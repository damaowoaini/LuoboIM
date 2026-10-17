package startrpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestOwnedRPCMongoUnknownAfterActualCloseRetainsNonpermitAndNeverExclusionDrain(t *testing.T) {
	for _, kind := range []string{"ack", "network-failed", "succeeded-WCE", "partial-start-unknown", "external-Kafka"} {
		t.Run(kind, func(t *testing.T) {
			input := ownedRPCInputFixture(t)
			registry := &ownedRPCRegistryFixture{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var closes atomic.Int32
			register := func(ctx context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
				source := ownedmongo.FromContext(ctx)
				if source == nil {
					return nil, errOwnedRPC
				}
				monitor := source.Monitor()
				if kind != "external-Kafka" {
					monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", ConnectionID: "must-not-retain-private-connection", RequestID: 1})
				}
				healthpb.RegisterHealthServer(server, health.NewServer())
				if err := OwnRPCDatabase(server, config.Config.Mongo.Database); err != nil {
					return nil, err
				}
				if err := OwnRPCResource(server, "redis", func(context.Context) error { closes.Add(1); return nil }); err != nil {
					return nil, err
				}
				trackErr := OwnRPCResource(server, "mongo", func(context.Context) error {
					closes.Add(1)
					if kind == "external-Kafka" {
						ownedsource.Attempt(ctx, ownedsource.KafkaProducer)(true)
						ownedsource.Attempt(ctx, ownedsource.KafkaProducer)(false)
						return nil
					}
					finish := event.CommandFinishedEvent{CommandName: "update", ConnectionID: "must-not-retain-private-connection", RequestID: 1}
					if kind == "network-failed" || kind == "partial-start-unknown" {
						monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: finish, Failure: "must-not-retain-private-error"})
					} else {
						reply := bson.D{{Key: "ok", Value: 1}}
						if kind == "succeeded-WCE" {
							reply = append(reply, bson.E{Key: "writeConcernError", Value: bson.D{{Key: "errmsg", Value: "must-not-retain-private-reply"}}})
						}
						raw, err := bson.Marshal(reply)
						if err != nil {
							return err
						}
						monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: finish, Reply: raw})
					}
					// A successful later command must never erase an earlier unknown write.
					monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", ConnectionID: "second", RequestID: 2})
					ack, _ := bson.Marshal(bson.D{{Key: "ok", Value: 1}})
					monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "update", ConnectionID: "second", RequestID: 2}, Reply: ack})
					return nil
				})
				if trackErr != nil {
					return nil, trackErr
				}
				if kind == "partial-start-unknown" {
					return nil, errors.New("original partial initialization failed")
				}
				return nil, nil
			}
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- startOwnedRPC(ctx, 0, "mongo-owned-fixture", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("original owner remained active")
				}
			})
			if kind != "partial-start-unknown" {
				awaitOwnedRPCAnnouncement(t, input)
				cancel()
			}
			var result error
			select {
			case result = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("actual owner did not close/join")
			}
			if kind == "partial-start-unknown" {
				if _, err := os.Lstat(input.path); !os.IsNotExist(err) {
					t.Fatal("partial startup wrote ready")
				}
			}
			if closes.Load() != 2 || registry.closes.Load() != 1 {
				t.Fatal("original resources were not actually closed before observation")
			}
			if kind == "ack" {
				if result != nil {
					t.Fatal(result)
				}
				value, _, err := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce)
				if err != nil || value.SourceOutcomeUnconfirmed {
					t.Fatal("normal ACK lost original drain semantics", err)
				}
				return
			}
			if !errors.Is(result, errOwnedRPC) {
				t.Fatal("unknown Mongo close produced success", result)
			}
			if _, err := os.Lstat(input.boot.DrainPath); !os.IsNotExist(err) {
				t.Fatal("unknown source wrote exclusion-shaped canonical drain", err)
			}
			raw, err := os.ReadFile(input.boot.DrainPath + ".unconfirmed")
			if err != nil {
				t.Fatal("private custody sidecar missing", err)
			}
			var envelope struct {
				OwnerNonce  string                         `json:"ownerNonce"`
				Observation ownedcontrol.SourceObservation `json:"observation"`
			}
			if kind == "external-Kafka" {
				if json.Unmarshal(raw, &envelope) != nil || envelope.OwnerNonce != input.nonce || envelope.Observation.BootstrapSHA256 != input.rawSHA || envelope.Observation.PolicySHA256 == "" || !envelope.Observation.SourceOutcomeUnconfirmed || envelope.Observation.Started != 0 || envelope.Observation.InFlight != 0 || envelope.Observation.External.KafkaProducer.Started != 2 || envelope.Observation.External.KafkaProducer.Unconfirmed != 1 || envelope.Observation.External.KafkaProducer.Succeeded != 1 || envelope.Observation.External.KafkaProducer.InFlight != 0 {
					t.Fatal("actual external source did not reach original owner gate/custody")
				}
			} else {
				if json.Unmarshal(raw, &envelope) != nil || envelope.OwnerNonce != input.nonce || envelope.Observation.BootstrapSHA256 != input.rawSHA || !envelope.Observation.SourceOutcomeUnconfirmed || envelope.Observation.Started != 2 || envelope.Observation.InFlight != 0 {
					t.Fatal("observation lost original owner or sticky unknown")
				}
			}
			if strings.Contains(string(raw), "must-not-retain") || strings.Contains(string(raw), "update") {
				t.Fatal("Mongo body/error/connection leaked into observation")
			}
		})
	}
}
