package ownedsource

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// These fixed names observe only actual dispatches, never authorization/Ready.
// No payload, URL, credential, error text, path or caller-supplied label is kept.
type Kind uint8

const (
	KafkaProducer Kind = iota
	PushDelivery
	ObjectStorageMutation
	RedisMutation
	kindCount
)

type Counts struct {
	Started     uint64 `json:"started"`
	Succeeded   uint64 `json:"succeeded"`
	Unconfirmed uint64 `json:"unconfirmed"`
	InFlight    int64  `json:"inFlight"`
	Unknown     bool   `json:"unknown"`
	Overflow    bool   `json:"overflow"`
}
type Snapshot struct {
	KafkaProducer         Counts `json:"kafkaProducer"`
	PushDelivery          Counts `json:"pushDelivery"`
	ObjectStorageMutation Counts `json:"objectStorageMutation"`
	RedisMutation         Counts `json:"redisMutation"`
	InvalidKind           bool   `json:"invalidKind"`
}

func (s Snapshot) Unconfirmed() bool {
	for _, c := range []Counts{s.KafkaProducer, s.PushDelivery, s.ObjectStorageMutation, s.RedisMutation} {
		if c.Unknown || c.Overflow || c.InFlight != 0 {
			return true
		}
	}
	return s.InvalidKind
}

type slot struct {
	started, succeeded, unconfirmed atomic.Uint64
	inFlight                        atomic.Int64
	unknown, overflow               atomic.Bool
}
type Observer struct {
	slots   [kindCount]slot
	invalid atomic.Bool
}
type key struct{}

var ErrOwner = errors.New("original source outcome owner is required")

func Bind(ctx context.Context, owner *Observer) (context.Context, error) {
	if ctx == nil || ctx.Done() == nil || owner == nil {
		return nil, ErrOwner
	}
	if previous := FromContext(ctx); previous != nil {
		if previous != owner {
			return nil, ErrOwner
		}
		return ctx, nil
	}
	return context.WithValue(ctx, key{}, owner), nil
}
func FromContext(ctx context.Context) *Observer {
	if ctx == nil {
		return nil
	}
	owner, _ := ctx.Value(key{}).(*Observer)
	return owner
}
func Attempt(ctx context.Context, kind Kind) func(bool) { return FromContext(ctx).Attempt(kind) }

// Keep this original observer in long-lived transport/producer constructors;
// a later caller context must not transfer an old operation to a new owner.
func (o *Observer) Attempt(kind Kind) func(bool) {
	if o == nil {
		return func(bool) {}
	}
	if kind >= kindCount {
		o.invalid.Store(true)
		return func(bool) {}
	}
	s := &o.slots[kind]
	if s.started.Add(1) == 0 {
		s.overflow.Store(true)
	}
	if s.inFlight.Add(1) <= 0 {
		s.overflow.Store(true)
	}
	var once sync.Once
	return func(unconfirmed bool) {
		once.Do(func() {
			// Publish sticky uncertainty before dropping in-flight. Snapshot reads
			// in-flight before unknown, so a completing unknown cannot look clear.
			if unconfirmed {
				s.unknown.Store(true)
				if s.unconfirmed.Add(1) == 0 {
					s.overflow.Store(true)
				}
			} else {
				if s.succeeded.Add(1) == 0 {
					s.overflow.Store(true)
				}
			}
			if s.inFlight.Add(-1) < 0 {
				s.overflow.Store(true)
			}
		})
	}
}
func (o *Observer) Snapshot() Snapshot {
	if o == nil {
		return Snapshot{}
	}
	read := func(kind Kind) Counts {
		s := &o.slots[kind]
		return Counts{Started: s.started.Load(), Succeeded: s.succeeded.Load(), Unconfirmed: s.unconfirmed.Load(), InFlight: s.inFlight.Load(), Unknown: s.unknown.Load(), Overflow: s.overflow.Load()}
	}
	return Snapshot{KafkaProducer: read(KafkaProducer), PushDelivery: read(PushDelivery), ObjectStorageMutation: read(ObjectStorageMutation), RedisMutation: read(RedisMutation), InvalidKind: o.invalid.Load()}
}
