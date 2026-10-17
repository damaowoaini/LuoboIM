package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

const testOperation = "3cc2f722-e20c-4de0-ad6b-6656ab6fe645"

func TestStorageFailurePreservesTransactionLabelsWithoutDriverDetails(t *testing.T) {
	cause := mongo.CommandError{Code: 112, Message: "synthetic-private-driver-detail", Labels: []string{"TransientTransactionError"}}
	err := unavailable(cause)
	var labeled interface{ HasErrorLabel(string) bool }
	if err.Error() != ErrUnavailable.Error() || !errors.Is(err, ErrUnavailable) || !errors.As(err, &labeled) || !labeled.HasErrorLabel("TransientTransactionError") {
		t.Fatal("safe storage error erased the transaction retry fact")
	}
	if !errors.Is(unavailable(context.Canceled), context.Canceled) {
		t.Fatal("storage error erased caller cancellation")
	}
}

func testCallback(t *testing.T) Frozen {
	t.Helper()
	body, err := json.Marshal(&callbackstruct.CallbackQuitGroupReq{CallbackCommand: callbackstruct.CallbackQuitGroupCommand, GroupID: "g", UserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewCallback(testOperation, 0, "g", "member", CallbackPayload{Name: AfterQuit, EndpointBinding: digest([]byte("synthetic endpoint config")), Body: body}, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestFrozenNotificationPreservesExactWireAndDoesNotBorrowCallerData(t *testing.T) {
	m := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", MsgFrom: constant.SysMsgType, SessionType: constant.SuperGroupChatType, ContentType: constant.GroupCreatedNotification, Content: []byte("synthetic notification"), ClientMsgID: "old", ServerMsgID: "old", Seq: 9, AtUserIDList: []string{"owner"}}
	f, err := NewNotification(testOperation, 1, "g", "owner", m, time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	m.Content[0] = 'X'
	m.AtUserIDList[0] = "changed"
	var frozen sdkws.MsgData
	if err := proto.Unmarshal(f.Payload, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.ClientMsgID != "ga3:"+f.ID || frozen.ServerMsgID != "" || frozen.Seq != 0 || frozen.CreateTime != 2000 || frozen.SendTime != 2000 || string(frozen.Content) != "synthetic notification" || frozen.AtUserIDList[0] != "owner" {
		t.Fatal("notification was retargeted or refrozen")
	}
	if m.ClientMsgID != "old" || m.ServerMsgID != "old" || m.Seq != 9 {
		t.Fatal("constructor mutated caller's message")
	}
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
}
func TestFrozenIdentitySeparatesOperationActorGroupAndOrdinal(t *testing.T) {
	base := testCallback(t)
	for _, tc := range []struct {
		operation, gid, actor string
		ordinal               int32
	}{{"42dd5689-54e7-4666-8fde-f5961b33fc67", "g", "member", 0}, {testOperation, "other", "member", 0}, {testOperation, "g", "other", 0}, {testOperation, "g", "member", 1}} {
		x, err := effectIdentity(tc.operation, tc.ordinal, tc.gid, tc.actor, AfterCallback, base.CreatedAt)
		if err != nil || x.ID == base.ID {
			t.Fatal("effect identity collided across intent scope")
		}
	}
	if _, err := effectIdentity("", 0, "g", "member", AfterCallback, base.CreatedAt); err == nil {
		t.Fatal("empty operation accepted")
	}
}
func TestFrozenPayloadCorruptionAndRetargetingAreRejected(t *testing.T) {
	base := testCallback(t)
	for name, change := range map[string]func(*Frozen){
		"hash":    func(f *Frozen) { f.PayloadSHA256 = digest([]byte("different")) },
		"body":    func(f *Frozen) { f.Payload = append([]byte(nil), f.Payload...); f.Payload[0] = 'x' },
		"group":   func(f *Frozen) { f.GroupID = "foreign" },
		"actor":   func(f *Frozen) { f.Actor = "foreign" },
		"ordinal": func(f *Frozen) { f.Ordinal++ },
		"kind":    func(f *Frozen) { f.Kind = Notification },
	} {
		t.Run(name, func(t *testing.T) {
			f := base
			change(&f)
			if f.validate() == nil {
				t.Fatal("corrupted/retargeted effect accepted")
			}
		})
	}
}
func TestTypedCallbackRejectsForeignCommandGroupAndDuplicateJSON(t *testing.T) {
	f := testCallback(t)
	var p CallbackPayload
	if json.Unmarshal(f.Payload, &p) != nil {
		t.Fatal("fixture")
	}
	foreign, _ := json.Marshal(&callbackstruct.CallbackQuitGroupReq{CallbackCommand: callbackstruct.CallbackQuitGroupCommand, GroupID: "foreign", UserID: "member"})
	for name, body := range map[string][]byte{
		"duplicate":     append(append([]byte(nil), p.Body[:len(p.Body)-1]...), []byte(`,"groupID":"g"}`)...),
		"foreign-group": foreign,
		"unknown-field": append(append([]byte(nil), p.Body[:len(p.Body)-1]...), []byte(`,"unknown":true}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			changed := p
			changed.Body = body
			if _, err := NewCallback(testOperation, 0, "g", "member", changed, f.CreatedAt); err == nil {
				t.Fatal("untyped callback body accepted")
			}
		})
	}
	p.Name = AfterKick
	if _, err := NewCallback(testOperation, 0, "g", "member", p, f.CreatedAt); err == nil {
		t.Fatal("logical callback destination changed")
	}
}
func TestNotificationCannotEscapeGroupOrBecomeUserMessage(t *testing.T) {
	base := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", MsgFrom: constant.SysMsgType, SessionType: constant.SuperGroupChatType, ContentType: constant.GroupCreatedNotification}
	for _, change := range []func(*sdkws.MsgData){func(m *sdkws.MsgData) { m.GroupID = "other" }, func(m *sdkws.MsgData) { m.RecvID = "other" }, func(m *sdkws.MsgData) { m.SendID = "other" }, func(m *sdkws.MsgData) { m.MsgFrom = constant.UserMsgType }, func(m *sdkws.MsgData) { m.ContentType = constant.FriendAddedNotification }} {
		m := proto.Clone(base).(*sdkws.MsgData)
		change(m)
		if _, err := NewNotification(testOperation, 0, "g", "owner", m, time.UnixMilli(1)); err == nil {
			t.Fatal("foreign notification/message accepted")
		}
	}
}
func TestDoneReceiptIsMinimalAndSummaryNeverExposesPayloadOrClaim(t *testing.T) {
	f := testCallback(t)
	r := Receipt{Frozen: f, State: Done, Revision: 3, Attempts: 1, Decision: "receiver-ack", OutcomeSHA256: digest([]byte("synthetic ack"))}
	r.Payload = nil
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	r.Payload = []byte("private request body")
	if r.validate() == nil {
		t.Fatal("done receipt retained private payload")
	}
	r.Payload = nil
	r.Claim = "3cc2f722-e20c-4de0-ad6b-6656ab6fe645"
	if r.validate() == nil {
		t.Fatal("done receipt retained dispatch capability")
	}
	r.State = Unconfirmed
	r.Payload = f.Payload
	r.ClaimedAt = time.UnixMilli(1001)
	encoded, err := json.Marshal(r.Summary())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(encoded, &fields) != nil {
		t.Fatal("summary JSON")
	}
	for _, key := range []string{"payload", "claim", "actor", "groupID", "uri", "callbackBody"} {
		if _, ok := fields[key]; ok {
			t.Fatal("summary exposed a private field", key)
		}
	}
}
func TestUnknownConfirmationRequiresExactRevisionPayloadAndIndependentEvidence(t *testing.T) {
	f := testCallback(t)
	valid := Confirmation{ExpectedSenderInstanceID: testOperation, ID: f.ID, ExpectedRevision: 3, ExpectedPayloadSHA256: f.PayloadSHA256, Decision: "not-applied", EvidenceSHA256: digest([]byte("remote cancelled")), DrainedEvidenceSHA256: digest([]byte("sender drained"))}
	if valid.validate() != nil {
		t.Fatal("valid explicit confirmation rejected")
	}
	for _, change := range []func(*Confirmation){func(c *Confirmation) { c.ID = "" }, func(c *Confirmation) { c.ExpectedSenderInstanceID = "" }, func(c *Confirmation) { c.ExpectedRevision = 0 }, func(c *Confirmation) { c.ExpectedPayloadSHA256 = "" }, func(c *Confirmation) { c.EvidenceSHA256 = "" }, func(c *Confirmation) { c.DrainedEvidenceSHA256 = "" }, func(c *Confirmation) { c.Decision = "retry" }} {
		c := valid
		change(&c)
		if c.validate() == nil {
			t.Fatal("inexact automatic replay confirmation accepted")
		}
	}
}
