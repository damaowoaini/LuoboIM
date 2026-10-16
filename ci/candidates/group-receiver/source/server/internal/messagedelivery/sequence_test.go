package messagedelivery

import (
	"context"
	"math"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

func sequenceFixture(t *testing.T) Envelope {
	t.Helper()
	m := &sdkws.MsgData{SendID: "sender", RecvID: "g", GroupID: "g", ServerMsgID: "stable-server-id", ClientMsgID: "stable-client-id", ContentType: constant.Text, MsgFrom: constant.UserMsgType, SessionType: constant.SuperGroupChatType, CreateTime: 2000, SendTime: 2000, Content: []byte("synthetic body"), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(true), msgprocessor.WithHistory(true))}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{ID: "86966e89-8467-4ff0-89b1-870e9b391f53", Branches: []Branch{{ID: "732d33c8-6198-4220-ac69-fec50f097a95", ConversationID: "sg_g", Payload: body, Recipients: []string{"z", "a"}, Persist: true}}}
}

func TestSequenceInputFreezesPayloadAndCanonicalRecipientsBeforeRetry(t *testing.T) {
	original := sequenceFixture(t)
	first, a, err := freezeEnvelope(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Branches) != 1 || first.Branches[0].Recipients[0] != "a" || original.Branches[0].Recipients[0] != "z" {
		t.Fatal("recipient snapshot borrowed or not canonical")
	}
	original.Branches[0].Recipients[0] = "changed"
	original.Branches[0].Payload[0] ^= 1
	_, b, err := freezeEnvelope(first)
	if err != nil || a != b {
		t.Fatal("prepared envelope changed after caller mutation", err)
	}
}

func TestSequenceInputRejectsChangedCIDPersistIdentityAndDuplicateBranches(t *testing.T) {
	for name, change := range map[string]func(*Envelope){"cid": func(e *Envelope) { e.Branches[0].ConversationID = "sg_other" }, "history": func(e *Envelope) { e.Branches[0].Persist = false }, "branch": func(e *Envelope) { e.Branches[0].ID = "" }, "source": func(e *Envelope) { e.ID = "" }, "duplicate-branch": func(e *Envelope) { e.Branches = append(e.Branches, e.Branches[0]) }, "recipient": func(e *Envelope) { e.Branches[0].Recipients = []string{"a", "a"} }, "payload": func(e *Envelope) { e.Branches[0].Payload = []byte("unknown") }} {
		t.Run(name, func(t *testing.T) {
			e := sequenceFixture(t)
			change(&e)
			if _, _, err := freezeEnvelope(e); err == nil {
				t.Fatal("inconsistent receiver admission accepted")
			}
		})
	}
}

func TestSequenceSeedRequiresBothAuthoritativeSourcesAndNeverChoosesLowerFloor(t *testing.T) {
	valid := Seed{MongoMax: 40, RedisMax: 70, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic drained raw floor evidence"))}
	if got, err := valid.maximum(); err != nil || got != 70 {
		t.Fatal("lower seed selected", err)
	}
	for _, change := range []func(*Seed){func(s *Seed) { s.MongoKnown = false }, func(s *Seed) { s.RedisKnown = false }, func(s *Seed) { s.MongoMax = -1 }, func(s *Seed) { s.RedisMax = math.MaxInt64 }, func(s *Seed) { s.EvidenceSHA256 = "" }} {
		s := valid
		change(&s)
		if _, err := s.maximum(); err == nil {
			t.Fatal("unknown or invalid history was adopted as an empty conversation")
		}
	}
}

func TestSequenceNamespacesSeparateEpochSourceBranchAndConversation(t *testing.T) {
	epoch := uuid.NewString()
	a := key(epoch, "sequence", "sg_g")
	for _, got := range []string{key(uuid.NewString(), "sequence", "sg_g"), key(epoch, "sequence", "n_g"), key(epoch, "branch", "sg_g"), key(epoch, "source", "sg_g")} {
		if got == a {
			t.Fatal("durable receiver key collision")
		}
	}
}

func TestSequenceNeverFallsBackToBareOrMetadataContext(t *testing.T) {
	if _, err := NewStore(nil, uuid.NewString(), hash([]byte("activation"))); err == nil {
		t.Fatal("nil receiver store accepted")
	}
	s := &Store{}
	if err := s.requireTX(context.WithValue(context.Background(), constant.OpUserID, "manager")); err == nil {
		t.Fatal("metadata forged strong sequence authority")
	}
	if _, err := s.Accept(context.Background(), sequenceFixture(t)); err == nil {
		t.Fatal("unprepared store accepted a normal message")
	}
}
