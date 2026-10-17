package ownedmongo

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

// Snapshot contains counts only. It is not a drain, permission, delivery ACK or
// exclusion permit. The original process identity/nonce is bound by its owner.
type Snapshot struct {
	External                 ownedsource.Snapshot `json:"external"`
	SourceOutcomeUnconfirmed bool                 `json:"sourceOutcomeUnconfirmed"`
	Started                  uint64               `json:"started"`
	Succeeded                uint64               `json:"succeeded"`
	Failed                   uint64               `json:"failed"`
	InFlight                 int                  `json:"inFlight"`
	Overflow                 bool                 `json:"overflow"`
}

type request struct {
	connection [32]byte
	request    int64
}
type Observer struct {
	external                   ownedsource.Observer
	mu                         sync.Mutex
	active                     map[request]struct{}
	started, succeeded, failed uint64
	unknown, overflow          bool
}

func mongoReadOnlyCommand(name string) bool {
	switch name {
	case "find", "getMore", "killCursors", "count", "distinct", "listIndexes", "listCollections", "listDatabases", "ping", "hello", "isMaster", "ismaster", "buildInfo", "getParameter", "saslStart", "saslContinue", "authenticate", "endSessions":
		return true
	default:
		return false // aggregate/unknown commands are conservative mutating candidates
	}
}
func (s *Observer) Monitor() *event.CommandMonitor {
	return &event.CommandMonitor{
		Started: func(_ context.Context, e *event.CommandStartedEvent) {
			if e == nil {
				s.mu.Lock()
				s.unknown = true
				s.mu.Unlock()
				return
			}
			if mongoReadOnlyCommand(e.CommandName) {
				return
			}
			key := request{sha256.Sum256([]byte(e.ConnectionID)), e.RequestID}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.started++
			if s.active == nil {
				s.active = make(map[request]struct{})
			}
			if _, exists := s.active[key]; exists {
				s.unknown = true
				return
			}
			if len(s.active) >= 1024 || len(e.ConnectionID) > 1024 {
				s.unknown = true
				s.overflow = true
				return
			}
			s.active[key] = struct{}{}
		},
		Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
			if e == nil {
				s.mu.Lock()
				s.unknown = true
				s.mu.Unlock()
				return
			}
			if mongoReadOnlyCommand(e.CommandName) {
				return
			}
			s.finish(e.CommandFinishedEvent, replyCommitUnconfirmed(e.Reply))
		},
		Failed: func(_ context.Context, e *event.CommandFailedEvent) {
			if e == nil {
				s.mu.Lock()
				s.unknown = true
				s.mu.Unlock()
				return
			}
			if mongoReadOnlyCommand(e.CommandName) {
				return
			}
			s.finish(e.CommandFinishedEvent, true) // includes network failure; never inspect Failure text
		},
	}
}
func (s *Observer) finish(e event.CommandFinishedEvent, unknown bool) {
	key := request{sha256.Sum256([]byte(e.ConnectionID)), e.RequestID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[key]; !exists {
		s.unknown = true
	} else {
		delete(s.active, key)
	}
	if unknown {
		s.unknown = true
		s.failed++
	} else {
		s.succeeded++
	}
}

// Mongo driver 1.17.10 emits Succeeded for WriteCommandError too. Only these
// two top-level error metadata fields are examined; command/query/data payloads
// and arbitrary error strings are never read or retained. A later success does
// not erase any prior unknown write/commit obligation.
func replyCommitUnconfirmed(reply bson.Raw) bool {
	value, err := reply.LookupErr("writeConcernError")
	if err == nil {
		return true
	} // any WCE shape remains conservatively unconfirmed
	if !errors.Is(err, bsoncore.ErrElementNotFound) {
		return true
	}
	value, err = reply.LookupErr("errorLabels")
	if errors.Is(err, bsoncore.ErrElementNotFound) {
		return false
	}
	if err != nil || value.Type != bsontype.Array {
		return true
	}
	labels, err := value.Array().Values()
	if err != nil || len(labels) > 16 {
		return true
	}
	for _, label := range labels {
		if label.Type != bsontype.String {
			return true
		}
		text := label.StringValue()
		if len(text) > 128 || text == "UnknownTransactionCommitResult" {
			return true
		}
	}
	return false
}
func (s *Observer) Snapshot() Snapshot {
	s.mu.Lock()
	value := Snapshot{SourceOutcomeUnconfirmed: s.unknown || len(s.active) != 0, Started: s.started, Succeeded: s.succeeded, Failed: s.failed, InFlight: len(s.active), Overflow: s.overflow}
	s.mu.Unlock()
	value.External = s.external.Snapshot()
	value.SourceOutcomeUnconfirmed = value.SourceOutcomeUnconfirmed || value.External.Unconfirmed()
	return value
}
