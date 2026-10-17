package groupeffects

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

// Serialization/branch compatibility only. This is deliberately not a fake
// DB/authority/namespace capability and does not prove any actual dispatch.
func applicationSourceData(t *testing.T) (memberApplicationDirect, *sdkws.MsgData) {
	t.Helper()
	epoch, command := uuid.NewString(), uuid.NewString()
	message := &sdkws.MsgData{ClientMsgID: uuid.NewString(), SendID: "owner", RecvID: "admin", ContentType: constant.JoinGroupApplicationNotification, SessionType: constant.SingleChatType, MsgFrom: constant.SysMsgType, CreateTime: 1000, Content: []byte(`{"detail":"sensitive-test-marker"}`), Options: config.GetOptionsByNotification(config.NotificationConf{IsSendMsg: true, ReliabilityLevel: constant.ReliableNotificationNoMsg}), OfflinePushInfo: &sdkws.OfflinePushInfo{}}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	cuts := map[string]deliveryprofile.OrdinaryVisibilityCut{}
	for _, branch := range memberApplicationBranches(message) {
		owners := map[string]int64{deliveryprofile.SHA([]byte(message.SendID)): 1, deliveryprofile.SHA([]byte(message.RecvID)): 2}
		normal := deliveryprofile.PrivacyScopeCut{ConversationID: msgprocessor.GetChatConversationIDByMsg(branch), SourceHead: 7, GlobalRevision: 3, Owners: owners}
		actual := normal
		actual.ConversationID = msgprocessor.GetConversationIDByMsg(branch)
		if actual.ConversationID != normal.ConversationID {
			actual.SourceHead = 11
		}
		cut := deliveryprofile.OrdinaryVisibilityCut{Version: "ordinary-visibility-cut-v1", Normal: normal, Branch: actual, Members: map[string]string{}}
		if !cut.Matches(branch) {
			t.Fatal("actual split branch data is not valid")
		}
		cuts[actual.ConversationID] = cut
	}
	cids := []string{msgprocessor.GetChatConversationIDByMsg(message), msgprocessor.GetNotificationConversationIDByMsg(message)}
	sort.Strings(cids)
	source := memberApplicationDirectSource{Proof: groupauthority.MemberApplicationMessageInfo{Command: groupauthority.NoticeCommandInfo{ID: command, Epoch: epoch, Actor: message.SendID, GroupID: "g", Action: groupauthority.NoticeAction(groupauthority.MemberApplicationCreated), CreatedAt: time.UnixMilli(1000).UTC()}, Kind: groupauthority.MemberApplicationCreated, Applicant: "applicant", Recipient: message.RecvID, MessageSHA256: digest(payload)}, Operation: "original-op", ServiceUser: "service", Activation: strings.Repeat("a", 64), Options: message.Options, Cuts: cuts, CIDs: cids}
	body, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	return memberApplicationDirect{ID: message.ClientMsgID, Kind: memberApplicationDirectKind, Source: source, SourceSHA: digest(body), Payload: payload, State: "unknown", Revision: 3, Claim: uuid.NewString(), AttemptedAt: time.UnixMilli(2000).UTC()}, message
}

func TestMemberApplicationRetiredPayloadKeepsMinimalUnknownCustodyWithoutRawContent(t *testing.T) {
	row, _ := applicationSourceData(t)
	if _, err := row.message(); err != nil {
		t.Fatal("live serialization baseline invalid", err)
	}
	original := row.SourceSHA
	row.Payload = []byte{}
	row.Redacted = true
	metadata, err := row.message()
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Content) != 0 || row.SourceSHA != original || row.State != "unknown" || row.Claim == "" {
		t.Fatal("retired source lost minimal original custody or retained body")
	}
	body, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sensitive-test-marker") {
		t.Fatal("retired source still persisted original content")
	}
	row.Payload = []byte("retained")
	if _, err = row.message(); err == nil {
		t.Fatal("redacted marker accepted a remaining plaintext body")
	}
	row.Payload = []byte{}
	row.State = "pending"
	if _, err = row.message(); err == nil {
		t.Fatal("retired source became fresh-send eligible")
	}
}

func TestMemberApplicationBranchCutPreservesOriginalSourceHeadAndExactSplitRoster(t *testing.T) {
	row, message := applicationSourceData(t)
	message.ServerMsgID = "actual-server-id"
	message.SendTime = 3000
	cap := &MemberApplicationKafkaSource{store: &Store{}, row: row, message: message}
	for _, branch := range memberApplicationBranches(message) {
		users := []string{message.SendID, message.RecvID}
		cut, err := cap.BranchCut(branch, users)
		if err != nil {
			t.Fatal(err)
		}
		if cut.Normal.SourceHead != 7 || len(cut.Normal.Owners) != 2 {
			t.Fatal("cut refreshed original head/owners")
		}
		cut.Normal.Owners[deliveryprofile.SHA([]byte(message.RecvID))] = 100
		again, err := cap.BranchCut(branch, users)
		if err != nil || again.Normal.Owners[deliveryprofile.SHA([]byte(message.RecvID))] != 2 {
			t.Fatal("caller mutated original private source cut", err)
		}
		if _, err = cap.BranchCut(branch, []string{message.RecvID}); err == nil {
			t.Fatal("partial roster borrowed complete source")
		}
		bad := proto.Clone(branch).(*sdkws.MsgData)
		bad.Content = []byte("changed")
		if _, err = cap.BranchCut(bad, users); err == nil {
			t.Fatal("another body borrowed source cut")
		}
		bad = proto.Clone(branch).(*sdkws.MsgData)
		bad.RecvID = "foreign"
		if _, err = cap.BranchCut(bad, users); err == nil {
			t.Fatal("another target borrowed source cut")
		}
	}
}
