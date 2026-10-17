package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"testing"
	"time"
)

func TestOwnedLegacyQuarantineDoesNotBlockKnownProfilesAndRedactionIsNotDelivery(t *testing.T) {
	ctx, s, db := receiverCutFixture(t, nil)
	if err := db.CreateCollection(ctx, groupeffects.Collection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
		t.Fatal(err)
	}
	effects, err := groupeffects.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	message := &sdkws.MsgData{ClientMsgID: uuid.NewString(), ServerMsgID: uuid.NewString(), SendID: "owner", MsgFrom: constant.SysMsgType, RecvID: "g", GroupID: "g", SessionType: constant.SuperGroupChatType, ContentType: constant.GroupMemberInfoSetNotification, Content: []byte("synthetic-notification"), CreateTime: time.Now().UnixMilli()}
	frozen, err := groupeffects.NewNotification(uuid.NewString(), 0, "g", "owner", message, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	policy := groupeffects.ActionDeliveryPolicy{Epoch: s.epoch, ConfigurationSHA256: hash([]byte("retained minimal policy"))}
	encoded, _ := json.Marshal(policy)
	receipt := groupeffects.Receipt{Frozen: frozen, State: groupeffects.Pending, Revision: 2, UpdatedAt: time.Now().UTC()}
	row, _ := bson.Marshal(receipt)
	var document bson.M
	if bson.Unmarshal(row, &document) != nil {
		t.Fatal("fixture BSON unavailable")
	}
	document["action_policy"] = policy
	document["action_policy_sha256"] = hash(encoded)
	document["delivery_roster_sha256"] = hash([]byte("original retained roster"))
	document["command_id"] = uuid.NewString()
	document["command_epoch"] = s.epoch
	document["producer_version"] = "group-role-v1"
	if _, err = db.Collection(groupeffects.Collection).InsertOne(ctx, document); err != nil {
		t.Fatal(err)
	}
	if required, err := effects.RequiredOwnedProfiles(ctx); err != nil || len(required) != 0 {
		t.Fatal("one old incomplete row blocked startup for valid new profiles", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.transaction(ctx, func(tx context.Context) error {
			quarantined, err := effects.QuarantineLegacyOwnedNotification(tx, frozen.ID)
			if err == nil && !quarantined {
				t.Fatal("old row not isolated")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := effects.InspectLegacyOwnedNotification(ctx, frozen.ID)
	if err != nil || !expected.Quarantined || expected.Revision != 3 {
		t.Fatal("quarantine was not durable/idempotent", err)
	}
	drain := OwnedLegacyDrainProof{Epoch: s.epoch, ActivationSHA256: s.activationSHA, LegacyProducersStopped: true, ReceiverWorkersJoined: true, EvidenceSHA256: hash([]byte("exact legacy producers and receiver joined"))}
	impossible := deliveryprofile.Group{}
	if err := s.ConfirmLegacyGroupNotification(ctx, expected, "restore-original-cut", &impossible, hash([]byte("cannot renew absent original cut")), drain); err == nil {
		t.Fatal("legacy plaintext gained a new current cut")
	}
	if err := s.ConfirmLegacyGroupNotification(ctx, expected, "redact-incomplete", nil, hash([]byte("explicit independent reconciliation")), drain); err != nil {
		t.Fatal(err)
	}
	result, err := effects.InspectLegacyOwnedNotification(ctx, frozen.ID)
	if err != nil || result.State != groupeffects.Done || result.Decision != "operator-redacted-incomplete" || result.PayloadSHA256 != expected.PayloadSHA256 || result.PolicySHA256 != expected.PolicySHA256 || result.RosterSHA256 != expected.RosterSHA256 {
		t.Fatal("redaction confused incomplete with delivered or lost audit hashes", err)
	}
	var after bson.M
	if err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"_id": frozen.ID}).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if _, exists := after["payload"]; exists {
		t.Fatal("legacy plaintext remained")
	}
	if _, exists := after["delivery_targets"]; exists {
		t.Fatal("legacy recipient IDs remained")
	}
	if err := s.ConfirmLegacyGroupNotification(ctx, expected, "redact-incomplete", nil, hash([]byte("stale")), drain); !errors.Is(err, groupeffects.ErrConflict) {
		t.Fatal("stale old receipt was reused", err)
	}
	count, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"source_id": frozen.ID})
	if err != nil || count != 0 {
		t.Fatal("redaction fabricated delivery/sequence receipt", err)
	}
}
