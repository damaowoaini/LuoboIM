package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
)

func TestOwnedMongoSourceSucceededWCEAndUnknownCommitCannotBeClearedByLaterSuccess(t *testing.T) {
	for _, item := range []struct {
		name   string
		reply  bson.D
		failed bool
	}{
		{"ack", bson.D{{Key: "ok", Value: 1}}, false},
		{"WCE", bson.D{{Key: "ok", Value: 1}, {Key: "writeConcernError", Value: bson.D{{Key: "errmsg", Value: "must-not-retain-private-text"}}}}, true},
		{"unknown commit", bson.D{{Key: "ok", Value: 1}, {Key: "errorLabels", Value: bson.A{"UnknownTransactionCommitResult"}}}, true},
		{"bad labels", bson.D{{Key: "ok", Value: 1}, {Key: "errorLabels", Value: true}}, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			source := &ownedMongoSource{}
			monitor := source.monitor()
			ctx := context.Background()
			start := event.CommandStartedEvent{CommandName: "commitTransaction", RequestID: 11, ConnectionID: "synthetic-owned-connection"}
			monitor.Started(ctx, &start)
			if source.observation().InFlight != 1 || !source.observation().SourceOutcomeUnconfirmed {
				t.Fatal("active write treated as drained")
			}
			raw, err := bson.Marshal(item.reply)
			if err != nil {
				t.Fatal(err)
			}
			monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: start.CommandName, RequestID: start.RequestID, ConnectionID: start.ConnectionID}, Reply: raw})
			if source.observation().SourceOutcomeUnconfirmed != item.failed {
				t.Fatal("server error metadata ignored")
			}
			start.RequestID++
			monitor.Started(ctx, &start)
			ack, _ := bson.Marshal(bson.D{{Key: "ok", Value: 1}})
			monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: start.CommandName, RequestID: start.RequestID, ConnectionID: start.ConnectionID}, Reply: ack})
			observation := source.observation()
			if observation.SourceOutcomeUnconfirmed != item.failed || observation.InFlight != 0 {
				t.Fatal("later success cleared original unknown")
			}
			body, _ := json.Marshal(observation)
			if strings.Contains(string(body), "private-text") || strings.Contains(string(body), "synthetic-owned-connection") || strings.Contains(string(body), "commitTransaction") {
				t.Fatal("raw server text/body/connection leaked into source evidence")
			}
		})
	}
}

func TestOwnedMongoSourceConnectionIsolationNetworkFailureAndReadFailureRemainDistinct(t *testing.T) {
	source := &ownedMongoSource{}
	monitor := source.monitor()
	ctx := context.Background()
	for _, connection := range []string{"synthetic-a", "synthetic-b"} {
		monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", RequestID: 7, ConnectionID: connection})
	}
	ack, _ := bson.Marshal(bson.D{{Key: "ok", Value: 1}})
	monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "update", RequestID: 7, ConnectionID: "synthetic-a"}, Reply: ack})
	if source.observation().InFlight != 1 {
		t.Fatal("same request ID on another connection was incorrectly released")
	}
	monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "update", RequestID: 7, ConnectionID: "synthetic-b"}, Failure: "must-not-read-or-store-error"})
	if !source.observation().SourceOutcomeUnconfirmed || source.observation().InFlight != 0 {
		t.Fatal("network write uncertainty lost")
	}
	read := &ownedMongoSource{}
	r := read.monitor()
	r.Started(ctx, &event.CommandStartedEvent{CommandName: "find", RequestID: 9, ConnectionID: "synthetic-a"})
	r.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", RequestID: 9, ConnectionID: "synthetic-a"}, Failure: "read-only-failed"})
	if read.observation().SourceOutcomeUnconfirmed || read.observation().Started != 0 {
		t.Fatal("known read failure fabricated an unconfirmed write")
	}
}
