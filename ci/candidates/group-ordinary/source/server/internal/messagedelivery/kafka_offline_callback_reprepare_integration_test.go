package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"math"
	"reflect"
	"testing"
)

func narrowOfflineCallbackFixture(t *testing.T, ctx context.Context, s *Store, change string) {
	t.Helper()
	if change == "owner-clear" {
		if err := s.MutateMessageVisibility(ctx, s.db, []table.PendingVisibilityChange{{ConversationID: "sg_g", Owners: []string{"member"}, Sequences: []int64{1}}}, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	} else {
		changed, err := s.db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "membership-window", "sg_g\x00member")}, bson.M{"$set": bson.M{"closed": true, "upper": int64(0), "last_command": uuid.NewString()}, "$inc": bson.M{"revision": int64(1), "group_revision": int64(1)}})
		if err != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			t.Fatal("exact membership close fixture failed", err)
		}
	}
}
func TestKafkaOfflineCallbackMongoKnownZeroHTTPReprepareOnlyNarrowsAndRetainsOldRequestReceipt(t *testing.T) {
	for _, state := range []string{"pending-not-dispatched", "pending-confirmed"} {
		for _, change := range []string{"owner-clear", "member-leave"} {
			t.Run(state+"/"+change, func(t *testing.T) {
				ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil, 1)
				first := true
				callbacks, posts := 0, 0
				a.prepare = func(work context.Context, call KafkaOfflineCall) error {
					d, body, err := callbackFixtureDispatch(work, s, call)
					if err != nil || d == nil {
						return err
					}
					if first {
						first = false
						if state == "pending-not-dispatched" {
							if err := d.NotDispatched(work); err != nil {
								return err
							}
							return errors.New("synthetic proven-zero-HTTP deferral")
						}
						if err := d.BeforeHTTP(work, call, body); err != nil {
							return err
						}
						callbacks++
						return d.Finish(work, "unknown", hash([]byte("synthetic original unknown response")), nil, nil)
					}
					var input struct{ Users []string }
					if err := json.Unmarshal(body, &input); err != nil {
						return err
					}
					if !reflect.DeepEqual(input.Users, []string{"extra-000"}) {
						t.Fatal("new request broadened or retained denied member")
					}
					if err := d.BeforeHTTP(work, call, body); err != nil {
						return err
					}
					callbacks++
					return d.Finish(work, "acknowledged", hash([]byte("synthetic safe narrowed response")), nil, nil)
				}
				a.provider = func(work context.Context, call KafkaOfflineCall, user string, d *KafkaAliasDispatch) error {
					if user != "extra-000" {
						t.Fatal("denied member reached supplier")
					}
					if err := d.BeforeProvider(work, call, user, d.row.Input.BodySHA256); err != nil {
						return err
					}
					posts++
					return d.Finish(work, KafkaAliasReport{PhysicalAttemptStarted: true, ProviderAccepted: true, ProviderReceiptSHA256: hash([]byte("synthetic provider ACK")), OutcomeSHA256: hash([]byte("synthetic outcome"))})
				}
				if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil {
					t.Fatal("first pending attempt unexpectedly complete")
				}
				original, err := s.InspectKafkaOfflineCallback(ctx, branch)
				if err != nil {
					t.Fatal(err)
				}
				if state == "pending-confirmed" {
					proof := KafkaOfflineCallbackConfirmation{Decision: "not-applied", EvidenceSHA256: hash([]byte("independent original no-effect evidence")), Drain: KafkaAttemptDrainProof{InstanceID: original.InstanceID, EvidenceSHA256: hash([]byte("original worker joined"))}}
					if err := s.ConfirmKafkaOfflineCallback(ctx, original, proof); err != nil {
						t.Fatal(err)
					}
				}
				old, err := s.InspectKafkaOfflineCallback(ctx, branch)
				if err != nil || old.State != state {
					t.Fatal("zero-HTTP state not established", err)
				}
				narrowOfflineCallbackFixture(t, ctx, s, change)
				if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err != nil {
					t.Fatal("safe narrowed retry remained permanently conflicted", err)
				}
				current, err := s.InspectKafkaOfflineCallback(ctx, branch)
				wantCalls := 1
				if state == "pending-confirmed" {
					wantCalls = 2
				}
				if err != nil || current.State != "acknowledged" || current.AttemptID == old.AttemptID || current.InputSHA256 == old.InputSHA256 || current.BodySHA256 == old.BodySHA256 || !validSHA(current.PreviousSnapshotSHA256) || posts != 1 || callbacks != wantCalls {
					t.Fatal("safe request or physical count was not exact", err)
				}
				var audit struct {
					Summary KafkaOfflineCallbackSummary `bson:"summary"`
					SHA     string                      `bson:"snapshot_sha256"`
				}
				if err := db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback-snapshot", current.PreviousSnapshotSHA256)}).Decode(&audit); err != nil || audit.SHA != current.PreviousSnapshotSHA256 || audit.Summary != old {
					t.Fatal("immutable original request receipt missing or changed", err)
				}
				if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"kind": "kafka-offline-callback-snapshot-v1", "branch_id": branch}); err != nil || n != 1 {
					t.Fatal("audit snapshot repeated/lost", err)
				}
			})
		}
	}
}
func TestKafkaOfflineCallbackMongoUnknownCannotBorrowNarrowedSnapshot(t *testing.T) {
	ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil, 1)
	callbacks := 0
	a.prepare = func(work context.Context, call KafkaOfflineCall) error {
		d, body, err := callbackFixtureDispatch(work, s, call)
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(work, call, body); err != nil {
			return err
		}
		callbacks++
		return d.Finish(work, "unknown", hash([]byte("synthetic unknown")), nil, nil)
	}
	a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error {
		t.Fatal("unknown reached supplier")
		return ErrInvalid
	}
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil {
		t.Fatal("unknown accepted")
	}
	before, err := s.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil {
		t.Fatal(err)
	}
	narrowOfflineCallbackFixture(t, ctx, s, "owner-clear")
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); !errors.Is(err, ErrKafkaPushCallbackUnconfirmed) || callbacks != 1 {
		t.Fatal("unknown callback retried after privacy change", err)
	}
	after, err := s.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil || before != after {
		t.Fatal("unknown original record rewritten", err)
	}
	if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"kind": "kafka-offline-callback-snapshot-v1"}); err != nil || n != 0 {
		t.Fatal("unknown got a replacement snapshot", err)
	}
}
func TestKafkaOfflineCallbackMongoRetryRevisionOverflowRefusesBeforeAnyReplacement(t *testing.T) {
	for _, revision := range []int64{math.MaxInt64 - 1, math.MaxInt64 - 2} {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {

			ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil)
			a.prepare = func(work context.Context, call KafkaOfflineCall) error {
				d, _, err := callbackFixtureDispatch(work, s, call)
				if err != nil || d == nil {
					return err
				}
				if err := d.NotDispatched(work); err != nil {
					return err
				}
				return errors.New("synthetic deferral")
			}
			a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error {
				t.Fatal("overflow reached supplier")
				return ErrInvalid
			}
			if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil {
				t.Fatal("deferral accepted")
			}
			if _, err := db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback", branch)}, bson.M{"$set": bson.M{"revision": int64(math.MaxInt64 - 1)}}); err != nil {
				t.Fatal(err)
			}
			before, err := s.InspectKafkaOfflineCallback(ctx, branch)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); !errors.Is(err, ErrConflict) {
				t.Fatal("max-1 retry created invalid max revision", err)
			}
			after, err := s.InspectKafkaOfflineCallback(ctx, branch)
			if err != nil || before != after {
				t.Fatal("overflow mutated original receipt", err)
			}
			if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"kind": "kafka-offline-callback-snapshot-v1"}); err != nil || n != 0 {
				t.Fatal("overflow left audit/write side effect", err)
			}

		})
	}
}
