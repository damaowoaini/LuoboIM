package msgtransfer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
)

func ownedMongoDrainRuntime(t *testing.T) *ManagedOrdinaryRuntime {
	t.Helper()
	b, path := managedOrdinaryBootstrapFixture(t)
	writeManagedBootstrapFixture(t, path, b)
	loaded, err := LoadOwnedOrdinaryBootstrap(b.Database)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := newManagedOrdinaryRuntime(cancel)
	r.bootstrap, r.instance, r.ownerNonce = loaded, uuid.NewString(), strings.Repeat("a", 64)
	var enteredOnce sync.Once
	entered := make(chan struct{})
	broker := &managedKafkaStub{consume: func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}, close: func() error { return nil }}
	intake, err := NewManagedOrdinaryKafka(intakeFixture(&intakeBoundaryStub{}), broker)
	if err != nil {
		t.Fatal(err)
	}
	if err := intake.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r.intake, r.intakeStarted = intake, true
	r.steps = []ordinaryStopStep{{"intake", intake.Stop}}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("actual intake not started")
	}
	adapter := &ownedWorkerAdapterFixture{policy: strings.Repeat("a", 64)}
	worker, err := newManagedKafkaOfflineWorker(&ownedWorkerStoreFixture{}, ownedWorkerReaderFixture{}, []OwnedKafkaOfflineTransport{adapter})
	if err != nil {
		t.Fatal(err)
	}
	r.offline = &OwnedOrdinaryOfflineRuntime{worker: worker, lifetime: ctx, cancel: cancel}
	r.steps = append(r.steps, ordinaryStopStep{"offline", r.offline.Stop})
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := r.Stop(end); err != nil {
			t.Error("original workers not joined", err)
		}
	})
	return r
}

func TestManagedOrdinaryMongoUnknownIsStickyAndWritesOnlyOriginalOwnerNonpermit(t *testing.T) {
	for _, kind := range []string{"in-flight", "failed-and-later-ACK", "partial-start-unknown"} {
		t.Run(kind, func(t *testing.T) {
			r := ownedMongoDrainRuntime(t)
			monitor := r.mongoSource.Monitor()
			ctx := context.Background()
			monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", ConnectionID: "must-not-retain", RequestID: 1})
			if kind != "in-flight" {
				monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "update", ConnectionID: "must-not-retain", RequestID: 1}, Failure: "must-not-retain"})
				monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", ConnectionID: "next", RequestID: 2})
				ack, _ := bson.Marshal(bson.D{{Key: "ok", Value: 1}})
				monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "update", ConnectionID: "next", RequestID: 2}, Reply: ack})
			}
			r.steps = append(r.steps, ordinaryStopStep{"mongo", func(context.Context) error { return nil }})
			end, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := r.Stop(end); err != nil {
				t.Fatal(err)
			}
			// The intake-only true custody case remains the original separate test.
			if kind == "partial-start-unknown" {
				r.intakeStarted = false // no complete-start identity is available
				if err := r.WriteUnconfirmedSource(end); !errors.Is(err, ErrManagedOrdinaryShutdownPending) {
					t.Fatal("partial Mongo source lost custody", err)
				}
			} else if _, err := r.WriteDrain(end); !errors.Is(err, ErrManagedOrdinaryShutdownPending) {
				t.Fatal("unknown Mongo produced a successful drain", err)
			}
			if _, err := os.Lstat(r.bootstrap.DrainPath); !os.IsNotExist(err) {
				t.Fatal("Mongo unknown wrote canonical drain", err)
			}
			raw, err := os.ReadFile(r.bootstrap.DrainPath + ".unconfirmed")
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				OwnerNonce  string                         `json:"ownerNonce"`
				Observation ownedcontrol.SourceObservation `json:"observation"`
			}
			if json.Unmarshal(raw, &envelope) != nil || envelope.OwnerNonce != r.ownerNonce || envelope.Observation.Role != "msgtransfer-managed" || envelope.Observation.BootstrapSHA256 != r.bootstrap.inputSHA || !envelope.Observation.SourceOutcomeUnconfirmed || strings.Contains(string(raw), "must-not-retain") {
				t.Fatal("source custody lost identity or leaked original data")
			}
		})
	}
}
