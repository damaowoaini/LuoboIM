package api

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/event"
)

func TestOwnedAPIPartialInitializationJoinsActualHandleBeforeUnknownObservation(t *testing.T) {
	source := &ownedMongoSource{}
	monitor := source.monitor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "createIndexes", ConnectionID: "synthetic-partial-client", RequestID: 1})
	owner := &apiOwnerFixture{}
	closed := false
	assembly := &ownedAPIAssembly{source: source, closeMongo: func(context.Context) error {
		closed = true
		monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "createIndexes", ConnectionID: "synthetic-partial-client", RequestID: 1}})
		return nil
	}}
	err := startOwnedAPI(ctx, 0, 0, owner, func(context.Context) (*ownedAPIAssembly, error) {
		return assembly, errors.New("actual index initialization failed")
	})
	if err == nil || !closed || owner.drained != nil || len(owner.unknown) != 1 || !owner.unknown[0].SourceOutcomeUnconfirmed {
		t.Fatal("partial startup lost actual close/unknown or emitted drain", err)
	}
}
