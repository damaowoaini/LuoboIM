package messagedelivery

import (
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

func TestNamespacePrincipalsKeepProtocolOriginsAcrossAmbiguousNames(t *testing.T) {
	tip := msgprocessor.WithOptions(msgprocessor.NewMsgOptions(), msgprocessor.WithNotNotification(false))
	group := &sdkws.MsgData{SendID: "sender", RecvID: "collision_peer", GroupID: "collision_peer", SessionType: constant.SuperGroupChatType, Options: tip}
	single := &sdkws.MsgData{SendID: "peer", RecvID: "collision", SessionType: constant.SingleChatType, Options: tip}
	server := &sdkws.MsgData{SendID: "z", RecvID: "a", SessionType: constant.NotificationChatType, Options: tip}
	g, err := DescribeNamespace(group)
	if err != nil {
		t.Fatal(err)
	}
	s, err := DescribeNamespace(single)
	if err != nil {
		t.Fatal(err)
	}
	if g.CID != "n_collision_peer" || s.CID != g.CID || g.Kind == s.Kind || g.SHA256 == s.SHA256 || len(g.OriginIDs) != 1 || g.OriginIDs[0] != "collision_peer" || len(s.OriginIDs) != 2 || s.OriginIDs[0] != "collision" || s.OriginIDs[1] != "peer" {
		t.Fatal("ambiguous names lost their independent typed origin")
	}
	n, err := DescribeNamespace(server)
	if err != nil || n.CID != "n_z_a" || n.OriginIDs[0] != "z" || n.OriginIDs[1] != "a" {
		t.Fatal("server notification order changed", err)
	}
	for _, name := range []string{"single-normal", "single-tip", "group-normal", "group-tip", "server-normal", "server-tip"} {
		t.Run(name, func(t *testing.T) {
			d := s
			switch name {
			case "single-normal":
				d.CID = "si_collision_peer"
			case "single-tip":
			case "group-normal":
				d, d.CID = g, "sg_collision_peer"
			case "group-tip":
				d = g
			case "server-normal":
				d, d.CID = n, "sn_z_a"
			case "server-tip":
				d = n
			}
			if err := validateNamespaceDescriptor(d); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"wrong-participants", "unsorted-single", "reordered-server", "foreign-CID", "foreign-group", "empty-ID", "extra-ID", "foreign-kind"} {
		t.Run(name, func(t *testing.T) {
			d := s
			d.OriginIDs = append([]string(nil), s.OriginIDs...)
			switch name {
			case "wrong-participants":
				d.OriginIDs[0] = "foreign"
			case "unsorted-single":
				d.OriginIDs[0], d.OriginIDs[1] = d.OriginIDs[1], d.OriginIDs[0]
			case "reordered-server":
				d = n
				d.OriginIDs = []string{"a", "z"}
			case "foreign-CID":
				d.CID = "n_unrelated"
			case "foreign-group":
				d = g
				d.GroupID = "other"
			case "empty-ID":
				d.OriginIDs[0] = ""
			case "extra-ID":
				d.OriginIDs = append(d.OriginIDs, "third")
			case "foreign-kind":
				d.Kind = "group"
			}
			if validateNamespaceDescriptor(d) == nil {
				t.Fatal("foreign origin descriptor accepted")
			}
		})
	}
}
