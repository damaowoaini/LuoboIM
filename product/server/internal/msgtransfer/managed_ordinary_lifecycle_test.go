package msgtransfer

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func TestManagedOrdinaryProcessStopJoinsInDependencyOrderAndIsIdempotent(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	r := newManagedOrdinaryRuntime(cancel)
	var order []string
	for _, name := range []string{"intake", "online", "offline", "online-callbacks", "discovery", "redis", "mongo"} {
		name := name
		r.steps = append(r.steps, ordinaryStopStep{name, func(context.Context) error { order = append(order, name); return nil }})
	}
	for i := 0; i < 2; i++ {
		if err := r.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(order, []string{"intake", "online", "offline", "online-callbacks", "discovery", "redis", "mongo"}) || !r.complete {
		t.Fatal("stop repeated or reordered ownership", order)
	}
	if _, err := r.DrainSummary(context.Background()); err == nil {
		t.Fatal("partial construction fabricated a complete-start drain")
	}
}

func TestManagedOrdinaryProcessPendingCloseRetainsPoolsAndJoinsTheSameAttempt(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	r := newManagedOrdinaryRuntime(cancel)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var closes, pools atomic.Int32
	r.steps = []ordinaryStopStep{{"intake", ordinaryCloseOnce(func() error { closes.Add(1); close(entered); <-release; return nil })}, {"mongo", func(context.Context) error { pools.Add(1); return nil }}}
	short, stop := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- r.Stop(short) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("owned close not entered")
	}
	stop()
	if err := <-first; !errors.Is(err, ErrManagedOrdinaryShutdownPending) {
		t.Fatal("unconfirmed close reported success", err)
	}
	if pools.Load() != 0 || closes.Load() != 1 || r.complete {
		t.Fatal("later pool closed beneath active intake")
	}
	close(release)
	end, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := r.Stop(end); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(end); err != nil || closes.Load() != 1 || pools.Load() != 1 {
		t.Fatal("same close could not be joined without repetition", err)
	}
}

func TestManagedOrdinaryProcessCloseErrorCannotAdvanceOrProduceDrain(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	r := newManagedOrdinaryRuntime(cancel)
	var pools atomic.Int32
	r.steps = []ordinaryStopStep{{"offline", ordinaryCloseOnce(func() error { return errors.New("synthetic pool close unconfirmed") })}, {"mongo", func(context.Context) error { pools.Add(1); return nil }}}
	for i := 0; i < 2; i++ {
		if err := r.Stop(context.Background()); !errors.Is(err, ErrManagedOrdinaryShutdownPending) {
			t.Fatal("close error accepted", err)
		}
	}
	if pools.Load() != 0 || r.complete || r.completed != 0 {
		t.Fatal("failed close advanced shutdown")
	}
	if _, err := r.DrainSummary(context.Background()); err == nil {
		t.Fatal("failed close produced drain")
	}
}

func TestManagedOrdinaryProcessUnstartedConsumerClosesOnceWithoutStartingIntake(t *testing.T) {
	var consumed, closed atomic.Int32
	broker := &managedKafkaStub{consume: func(context.Context, []string, sarama.ConsumerGroupHandler) error { consumed.Add(1); return nil }, close: func() error { closed.Add(1); return nil }}
	m, err := NewManagedOrdinaryKafka(intakeFixture(&intakeBoundaryStub{}), broker)
	if err != nil {
		t.Fatal(err)
	}
	if err := stopUnstartedOrdinaryIntake(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if err := stopUnstartedOrdinaryIntake(context.Background(), m); err != nil || closed.Load() != 1 || consumed.Load() != 0 {
		t.Fatal("partial assembly started or double-closed consumer", err)
	}
	if err := m.Stop(context.Background()); !errors.Is(err, messagedelivery.ErrUnprepared) {
		t.Fatal("construction cleanup became a successful intake start")
	}
}
