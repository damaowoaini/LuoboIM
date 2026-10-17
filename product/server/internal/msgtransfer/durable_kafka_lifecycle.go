package msgtransfer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

var ErrOrdinaryKafkaShutdownPending = errors.New("ordinary Kafka shutdown is unconfirmed; retain owned pools")

// ManagedOrdinaryKafka owns only the intake consumer lifecycle. It does not
// claim that projection/offline/vendor/Seq0 workers are implemented. The old
// StartTransfer required-mode fence therefore remains. Pool owners must wait
// for Stop success before closing Mongo/Redis or retiring the process.
type ManagedOrdinaryKafka struct {
	intake      *DurableKafkaIntake
	consumer    sarama.ConsumerGroup
	handler     *OnlineHistoryRedisConsumerHandler
	mu          sync.Mutex
	started     bool
	cancel      context.CancelFunc
	done        chan struct{}
	closeDone   chan struct{}
	custodyDone chan struct{}
	closeOnce   sync.Once
	closeErr    error
	runErr      error
}

func NewManagedOrdinaryKafka(intake *DurableKafkaIntake, consumer sarama.ConsumerGroup) (*ManagedOrdinaryKafka, error) {
	if intake == nil || consumer == nil {
		return nil, messagedelivery.ErrInvalid
	}
	handler, err := NewDurableOnlineHistoryHandler(intake)
	if err != nil {
		return nil, err
	}
	return &ManagedOrdinaryKafka{intake: intake, consumer: consumer, handler: handler, done: make(chan struct{}), closeDone: make(chan struct{})}, nil
}

func (m *ManagedOrdinaryKafka) Start(parent context.Context) error {
	if m == nil || parent == nil {
		return messagedelivery.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return messagedelivery.ErrConflict
	}
	// Required catalog/epoch/window readiness is checked here, not inferred
	// from a successful non-I/O constructor. This still is not whole-runtime
	// readiness: the still-closed StartTransfer gate is a separate requirement.
	ready, cancelReady := context.WithTimeout(parent, 10*time.Second)
	defer cancelReady()
	if err := m.intake.CheckReady(ready); err != nil {
		return err
	}
	if err := parent.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.started = true
	if m.intake.custody != nil {
		m.custodyDone = make(chan struct{})
		go func() {
			defer close(m.custodyDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				work, stop := context.WithTimeout(ctx, 10*time.Second)
				err := m.intake.custody.Expire(work, 32)
				stop()
				if err != nil {
					if ctx.Err() == nil {
						m.intake.recordFailure(err)
						cancel()
					}
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	go func() {
		defer close(m.done)
		for ctx.Err() == nil {
			if err := m.intake.failure(); err != nil {
				m.setRunError(err)
				return
			}
			if err := m.consumer.Consume(ctx, []string{m.intake.topic}, m.handler); err != nil {
				if ctx.Err() == nil {
					m.setRunError(err)
				}
				cancel()
				return
			}
		}
	}()
	go func() {
		select {
		case <-m.intake.failed:
			cancel()
		case <-m.done:
		case <-ctx.Done():
		}
	}()
	return nil
}

func (m *ManagedOrdinaryKafka) setRunError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runErr == nil {
		m.runErr = err
	}
}

// Close is not called twice, and an early deadline never creates a success
// stamp. A later Stop may continue waiting for the SAME closing operation;
// it does not start another consumer or replay an uncertain record.
func (m *ManagedOrdinaryKafka) Stop(ctx context.Context) error {
	if m == nil || ctx == nil {
		return messagedelivery.ErrInvalid
	}
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return messagedelivery.ErrUnprepared
	}
	cancel := m.cancel
	m.mu.Unlock()
	cancel()
	m.closeOnce.Do(func() {
		go func() {
			err := m.consumer.Close()
			m.mu.Lock()
			m.closeErr = err
			m.mu.Unlock()
			close(m.closeDone)
		}()
	})
	select {
	case <-m.done:
	case <-ctx.Done():
		return ErrOrdinaryKafkaShutdownPending
	}
	if m.custodyDone != nil {
		select {
		case <-m.custodyDone:
		case <-ctx.Done():
			return ErrOrdinaryKafkaShutdownPending
		}
	}
	select {
	case <-m.closeDone:
	case <-ctx.Done():
		return ErrOrdinaryKafkaShutdownPending
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeErr
}

// Intake outcome is distinct from shutdown outcome. A known joined/closed
// consumer may release its pools while its original source remains unknown;
// the failure/receipt is retained for inspection and a fresh-instance resolve.
func (m *ManagedOrdinaryKafka) Failure() error {
	if m == nil {
		return messagedelivery.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return errors.Join(m.runErr, m.intake.failure())
}
