package kafka

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/IBM/sarama"
)

// One actual Consume loop/handler belongs to one original process. Setup proves
// real broker assignment; Claim waits for the original admission gate. Closing
// resources cannot overtake an unjoined claim. Rebalance keeps the same owner.
type OwnedConsumerRun struct {
	consumer                        sarama.ConsumerGroup
	topics                          []string
	handler                         sarama.ConsumerGroupHandler
	ctx                             context.Context
	cancel                          context.CancelFunc
	mu                              sync.Mutex
	started, admitted, stopping     bool
	ready, admission, done          chan struct{}
	readyOnce, admitOnce, closeOnce sync.Once
	closeDone                       chan struct{}
	errorStop, errorDone            chan struct{}
	failure, closeErr               error
	ownerCancel                     context.CancelFunc
}

func NewOwnedConsumerRun(parent context.Context, consumer sarama.ConsumerGroup, topics []string, handler sarama.ConsumerGroupHandler, ownerCancel context.CancelFunc) (*OwnedConsumerRun, error) {
	if parent == nil || parent.Done() == nil || consumer == nil || handler == nil || len(topics) != 1 || topics[0] == "" || ownerCancel == nil {
		return nil, ErrOwnedConsumer
	}
	ctx, cancel := context.WithCancel(parent)
	return &OwnedConsumerRun{consumer: consumer, topics: append([]string{}, topics...), handler: handler, ctx: ctx, cancel: cancel, ready: make(chan struct{}), admission: make(chan struct{}), done: make(chan struct{}), closeDone: make(chan struct{}), errorStop: make(chan struct{}), errorDone: make(chan struct{}), ownerCancel: ownerCancel}, nil
}
func (r *OwnedConsumerRun) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrOwnedConsumer
	}
	r.mu.Lock()
	if r.started || r.stopping {
		r.mu.Unlock()
		return ErrOwnedConsumer
	}
	r.started = true
	r.mu.Unlock()
	go r.watchErrors()

	go func() {
		defer close(r.done)
		for r.ctx.Err() == nil {
			err := r.consumer.Consume(r.ctx, r.topics, ownedConsumerGate{r})
			if err != nil && r.ctx.Err() == nil {
				r.mu.Lock()
				r.failure = ErrOwnedConsumer
				r.mu.Unlock()
				r.cancel()
				r.ownerCancel()
				return
			}
		}
	}()
	select {
	case <-r.ready:
		return nil
	case <-r.done:
		return errors.Join(ErrOwnedConsumer, r.Failure())
	case <-ctx.Done():
		r.cancel()
		return ctx.Err()
	case <-r.ctx.Done():
		return ErrOwnedConsumer
	}
}
func (r *OwnedConsumerRun) Enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.admitted || r.stopping || r.ctx.Err() != nil || r.failure != nil {
		return ErrOwnedConsumer
	}
	select {
	case <-r.ready:
	default:
		return ErrOwnedConsumer
	}
	r.admitted = true
	r.admitOnce.Do(func() { close(r.admission) })
	return nil
}
func (r *OwnedConsumerRun) StopAdmission(context.Context) error {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
	r.cancel()
	return nil
}
func (r *OwnedConsumerRun) Failure() error { r.mu.Lock(); defer r.mu.Unlock(); return r.failure }
func (r *OwnedConsumerRun) Stop(ctx context.Context) error {
	if ctx == nil {
		return ErrOwnedConsumer
	}
	_ = r.StopAdmission(ctx)
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if started {
		select {
		case <-r.done:
		case <-ctx.Done():
			return errors.Join(ErrOwnedConsumer, ctx.Err())
		}
	}
	r.closeOnce.Do(func() {
		go func() {
			r.closeErr = r.consumer.Close()
			if started {
				close(r.errorStop)
				<-r.errorDone
			}
			close(r.closeDone)
		}()
	})
	select {
	case <-r.closeDone:
		return r.closeErr
	case <-ctx.Done():
		return errors.Join(ErrOwnedConsumer, ctx.Err())
	}
}

type ownedConsumerGate struct{ owner *OwnedConsumerRun }

func (g ownedConsumerGate) Setup(s sarama.ConsumerGroupSession) error {
	if err := g.owner.handler.Setup(s); err != nil {
		return err
	}
	g.owner.readyOnce.Do(func() { close(g.owner.ready) })
	return nil
}
func (g ownedConsumerGate) Cleanup(s sarama.ConsumerGroupSession) error {
	return g.owner.handler.Cleanup(s)
}
func (g ownedConsumerGate) ConsumeClaim(s sarama.ConsumerGroupSession, c sarama.ConsumerGroupClaim) error {
	select {
	case <-g.owner.admission:
	case <-g.owner.ctx.Done():
		return g.owner.ctx.Err()
	case <-s.Context().Done():
		return s.Context().Err()
	}
	if err := g.owner.ctx.Err(); err != nil {
		return err
	}
	return g.owner.handler.ConsumeClaim(s, c)
}

// Actual startup gets a finite caller bound without installing a new retry or
// throwing away the original owner. Stop still has to join a timed-out Start.
func (r *OwnedConsumerRun) StartBounded(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	return r.Start(ctx)
}

func (r *OwnedConsumerRun) Admitted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.admitted && !r.stopping && r.ctx.Err() == nil && r.failure == nil
}

func (r *OwnedConsumerRun) Fail() {
	r.mu.Lock()
	if r.failure == nil {
		r.failure = ErrOwnedConsumer
	}
	r.mu.Unlock()
	r.cancel()
	r.ownerCancel()
}

// Offset/claim errors must not disappear into Sarama's optional log sink. This
// one watcher remains owned until the actual group Close finishes; no error
// worker or buffered unknown can survive a successful source-owner drain.
func (r *OwnedConsumerRun) watchErrors() {
	defer close(r.errorDone)
	issues := r.consumer.Errors()
	accept := func(err error) {
		if err == nil {
			return
		}
		r.mu.Lock()
		stopping := r.stopping
		r.mu.Unlock()
		if stopping && errors.Is(err, context.Canceled) {
			return
		}
		r.Fail()
	}
	for {
		select {
		case err, ok := <-issues:
			if !ok {
				return
			}
			accept(err)
		case <-r.errorStop:
			for {
				select {
				case err, ok := <-issues:
					if !ok {
						return
					}
					accept(err)
				default:
					return
				}
			}
		}
	}
}
