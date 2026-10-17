package deliveryprofile

import (
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"testing"
)

func TestOrdinaryVisibilityCutUsesTypedCIDAndStrictSnapshot(t *testing.T) {
	for _, tt := range []struct {
		name            string
		session         int32
		send, recv, gid string
	}{
		{"single-sorted", constant.SingleChatType, "z", "a", ""},
		{"server-notification-ordered", constant.NotificationChatType, "z", "a", ""},
		{"group-original-ID", constant.SuperGroupChatType, "owner", "sg_g_under", "sg_g_under"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &sdkws.MsgData{SessionType: tt.session, SendID: tt.send, RecvID: tt.recv, GroupID: tt.gid, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false), msgprocessor.WithHistory(false))}
			n, b, g, e := ordinaryCutCIDs(m)
			if e != nil {
				t.Fatal(e)
			}
			if n != msgprocessor.GetChatConversationIDByMsg(m) || b != msgprocessor.GetNotificationConversationIDByMsg(m) {
				t.Fatal("CID algorithm changed")
			}
			if tt.session == constant.NotificationChatType && (n != "sn_z_a" || b != "n_z_a") {
				t.Fatal("system principal order sorted")
			}
			owners := map[string]int64{SHA([]byte(m.SendID)): 0, SHA([]byte(m.RecvID)): 0}
			members := map[string]string{}
			if g != "" {
				owners = map[string]int64{SHA([]byte(m.SendID)): 0}
				members[SHA([]byte(m.SendID))] = SHA([]byte("original-membership-command"))
			}
			c := OrdinaryVisibilityCut{Version: "ordinary-visibility-cut-v1", Normal: PrivacyScopeCut{ConversationID: n, Owners: owners}, Branch: PrivacyScopeCut{ConversationID: b, Owners: owners}, MembershipCID: g, Members: members}
			if !c.Valid() || !c.Matches(m) {
				t.Fatal("exact original cut rejected")
			}
			raw, e := bson.Marshal(c)
			if e != nil {
				t.Fatal(e)
			}
			var round OrdinaryVisibilityCut
			if e = bson.Unmarshal(raw, &round); e != nil || Digest(round) != Digest(c) {
				t.Fatal("valid cut roundtrip", e)
			}
			m.RecvID = "foreign"
			if round.Matches(m) {
				t.Fatal("foreign typed origin accepted")
			}
			var malformed bson.D
			if e = bson.Unmarshal(raw, &malformed); e != nil {
				t.Fatal(e)
			}
			malformed = append(malformed, bson.E{Key: "version", Value: c.Version})
			bad, _ := bson.Marshal(malformed)
			if bson.Unmarshal(bad, &round) == nil {
				t.Fatal("duplicate custody field accepted")
			}
		})
	}
}
