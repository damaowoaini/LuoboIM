package msgtransfer

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func TestManagedOrdinaryDrainUsesActualJoinsPreservesUnknownAndNeverClobbers(t *testing.T) {
	b, path := managedOrdinaryBootstrapFixture(t)
	writeManagedBootstrapFixture(t, path, b)
	loaded, err := LoadOwnedOrdinaryBootstrap(b.Database)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := newManagedOrdinaryRuntime(cancel)
	r.bootstrap, r.instance = loaded, uuid.NewString()
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := r.Stop(end); err != nil {
			t.Error("actual workers failed cleanup", err)
		}
	})
	entered := make(chan struct{})
	var consumerClosed atomic.Int32
	broker := &managedKafkaStub{consume: func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}, close: func() error { consumerClosed.Add(1); return nil }}
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
		t.Fatal("actual consumer not entered")
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
	if _, err := r.WriteDrain(context.Background()); err == nil {
		t.Fatal("active worker produced a complete drain")
	}
	intake.intake.recordFailure(messagedelivery.ErrCommitUnconfirmed)
	end, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := r.Stop(end); err != nil {
		t.Fatal(err)
	}
	digest, err := r.WriteDrain(end)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(b.DrainPath)
	if err != nil {
		t.Fatal(err)
	}
	var summary ManagedOrdinaryDrain
	if json.Unmarshal(raw, &summary) != nil || summary.Version != "managed-ordinary-drained-v1" || summary.InstanceID != r.instance || summary.BootstrapSHA256 != loaded.inputSHA || !summary.SourceOutcomeUnconfirmed || summary.Offline.InstanceID != worker.store.KafkaOfflineInstanceID() || deliveryprofile.SHA(raw) != digest {
		t.Fatal("drain did not bind the original lifecycle and unknown state")
	}
	if consumerClosed.Load() != 1 || adapter.closed.Load() != 1 {
		t.Fatal("worker shutdown did not close each owned boundary once")
	}
	if _, err := r.WriteDrain(end); err == nil {
		t.Fatal("existing drain receipt was overwritten")
	}
	after, err := os.ReadFile(b.DrainPath)
	if err != nil || string(after) != string(raw) {
		t.Fatal("failed second write changed original drain")
	}
}
