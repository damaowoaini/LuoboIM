package ownedsource_test

import (
	"context"
	"sync"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

func TestOriginalOwnedSourceSurvivesOwnerReplacementConcurrentFinishAndLaterACK(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	old, next := &ownedmongo.Observer{}, &ownedmongo.Observer{}
	first, err := ownedmongo.Bind(parent, old)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ownedmongo.Bind(parent, next)
	if err != nil {
		t.Fatal(err)
	}
	if ownedsource.FromContext(first) == nil || ownedsource.FromContext(first) == ownedsource.FromContext(second) {
		t.Fatal("owner-bound sources were not independent")
	}
	if _, err := ownedsource.Bind(first, ownedsource.FromContext(second)); err == nil {
		t.Fatal("replacement context erased original source")
	}
	finish := ownedsource.Attempt(first, ownedsource.KafkaProducer)
	if !old.Snapshot().SourceOutcomeUnconfirmed {
		t.Fatal("actual in-flight source looked drained")
	}
	var wg sync.WaitGroup
	wg.Add(8)
	for i := 0; i < 8; i++ {
		go func() { defer wg.Done(); finish(true) }()
	}
	wg.Wait()
	ownedsource.Attempt(first, ownedsource.KafkaProducer)(false)
	oldSnapshot, nextSnapshot := old.Snapshot(), next.Snapshot()
	if !oldSnapshot.SourceOutcomeUnconfirmed || oldSnapshot.External.KafkaProducer.Unconfirmed != 1 || oldSnapshot.External.KafkaProducer.Succeeded != 1 || oldSnapshot.External.KafkaProducer.InFlight != 0 || nextSnapshot.SourceOutcomeUnconfirmed {
		t.Fatal("later ACK/new owner erased or duplicated original unknown")
	}
	// Every finite source is local observation only; an unknown kind cannot
	// create a new scope or disappear as a successful source.
	ownedsource.Attempt(second, ownedsource.Kind(255))(false)
	if !next.Snapshot().SourceOutcomeUnconfirmed || !next.Snapshot().External.InvalidKind {
		t.Fatal("unknown source kind silently authorized clear state")
	}
}
