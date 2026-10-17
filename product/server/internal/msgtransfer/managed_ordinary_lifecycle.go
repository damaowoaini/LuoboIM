package msgtransfer

import (
	"context"
	"errors"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
)

var ErrManagedOrdinaryShutdownPending = errors.New("managed ordinary shutdown unconfirmed; retain owned resources and original receipts")

type ordinaryStopStep struct {
	name string
	stop func(context.Context) error
}

// One assembly owns its one consumer and recovery workers. A bounded caller
// may stop waiting, but never starts a second Close or closes a later shared
// pool beneath a worker whose join is still unconfirmed.
type ManagedOrdinaryRuntime struct {
	namespaceGate *ownedcontrol.NamespaceRuntimeStartupGate
	cancel        context.CancelFunc
	intake        *ManagedOrdinaryKafka
	offline       *OwnedOrdinaryOfflineRuntime
	bootstrap     OwnedOrdinaryBootstrap
	instance      string
	stopLock      chan struct{}
	steps         []ordinaryStopStep
	completed     int
	complete      bool
	intakeStarted bool
	unexpected    <-chan error
	mongoSource   *ownedmongo.Observer
	ownerNonce    string
}

func newManagedOrdinaryRuntime(cancel context.CancelFunc) *ManagedOrdinaryRuntime {
	r := &ManagedOrdinaryRuntime{cancel: cancel, stopLock: make(chan struct{}, 1), mongoSource: &ownedmongo.Observer{}}
	r.stopLock <- struct{}{}
	return r
}

func (r *ManagedOrdinaryRuntime) Stop(ctx context.Context) error {
	if r == nil || ctx == nil || r.cancel == nil {
		return messagedelivery.ErrInvalid
	}
	select {
	case <-ctx.Done():
		return ErrManagedOrdinaryShutdownPending
	case <-r.stopLock:
	}
	defer func() { r.stopLock <- struct{}{} }()
	if r.namespaceGate != nil {
		r.namespaceGate.Retire()
	}
	r.cancel()
	for r.completed < len(r.steps) {
		if ctx.Err() != nil {
			return ErrManagedOrdinaryShutdownPending
		}
		if err := r.steps[r.completed].stop(ctx); err != nil {
			return errors.Join(ErrManagedOrdinaryShutdownPending, err)
		}
		r.completed++
	}
	r.complete = true
	return nil
}

// Wrap non-context Close methods once. Timeout is an unconfirmed wait, never
// permission to re-close a pool or advance the dependent shutdown sequence.
func ordinaryCloseOnce(closeFn func() error) func(context.Context) error {
	var once sync.Once
	done := make(chan struct{})
	var result error
	return func(ctx context.Context) error {
		if ctx == nil {
			return messagedelivery.ErrInvalid
		}
		once.Do(func() { go func() { result = closeFn(); close(done) }() })
		select {
		case <-ctx.Done():
			return ErrManagedOrdinaryShutdownPending
		case <-done:
			return result
		}
	}
}

func stopUnstartedOrdinaryIntake(ctx context.Context, m *ManagedOrdinaryKafka) error {
	if m == nil || ctx == nil {
		return messagedelivery.ErrInvalid
	}
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
	case <-ctx.Done():
		return ErrManagedOrdinaryShutdownPending
	case <-m.closeDone:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeErr
}

func (r *ManagedOrdinaryRuntime) Wait(ctx context.Context) error {
	if r == nil || ctx == nil || r.intake == nil || !r.intakeStarted {
		return messagedelivery.ErrUnprepared
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-r.unexpected:
		return err
	case <-r.intake.done:
		if err := r.intake.Failure(); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("managed ordinary intake stopped unexpectedly")
	}
}
