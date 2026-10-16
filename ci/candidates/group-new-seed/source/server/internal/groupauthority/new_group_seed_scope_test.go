package groupauthority

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

func TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope(t *testing.T) {
	old := config.Config
	t.Cleanup(func() { config.Config = old })
	config.Config.Manager.UserID = []string{"seed-manager"}
	config.Config.IMAdmin.UserID = nil
	group := &relation.GroupModel{GroupID: "sg_a_b", GroupType: constant.WorkingGroup, CreatorUserID: "owner", CreateTime: time.Unix(1, 123456).UTC()}
	rows := []*relation.GroupMemberModel{{GroupID: group.GroupID, UserID: "owner", RoleLevel: constant.GroupOwner, JoinTime: group.CreateTime}, {GroupID: group.GroupID, UserID: "member", RoleLevel: constant.GroupOrdinaryUsers, JoinTime: group.CreateTime}}
	epoch, activation := uuid.NewString(), strings.Repeat("a", 64)
	for _, actor := range []string{"owner", "seed-manager"} {
		t.Run(actor, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), constant.OpUserID, actor)
			bound, info, err := NewGroupCreationCommand(ctx, epoch, activation, "owner", group, rows)
			if err != nil || info.Actor != actor || info.Owner != "owner" || info.GroupID != group.GroupID || info.ID == "" {
				t.Fatal("legal creator did not obtain immutable server command", err)
			}
			info.Members[0].UserID = "foreign"
			if bound.Value(creationCommandKey{}).(*creationCommand).info.Members[0].UserID == "foreign" {
				t.Fatal("returned command slice changed private authority")
			}
			if _, _, err := NewGroupCreationCommand(bound, epoch, activation, "owner", group, rows); err == nil {
				t.Fatal("same command context silently replaced its identity")
			}
			if _, err := CaptureNewGroupSeed(bound, nil, group.GroupID); err == nil {
				t.Fatal("command alone minted live writer proof")
			}
		})
	}
	t.Run("foreign-owner", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), constant.OpUserID, "foreign")
		if _, _, err := NewGroupCreationCommand(ctx, epoch, activation, "owner", group, rows); err == nil {
			t.Fatal("foreign ordinary caller captured an owner creation command")
		}
	})
	for _, invalid := range []string{"duplicate", "second-owner", "unknown-role", "dismissed"} {
		t.Run(invalid, func(t *testing.T) {
			g := *group
			a, b := *rows[0], *rows[1]
			members := []*relation.GroupMemberModel{&a, &b}
			switch invalid {
			case "duplicate":
				b.UserID = a.UserID
			case "second-owner":
				b.RoleLevel = constant.GroupOwner
			case "unknown-role":
				b.RoleLevel = 45
			case "dismissed":
				g.Status = constant.GroupStatusDismissed
			}
			ctx := context.WithValue(context.Background(), constant.OpUserID, "owner")
			if _, _, err := NewGroupCreationCommand(ctx, epoch, activation, "owner", &g, members); err == nil {
				t.Fatal("invalid initial membership/terminal state captured authority")
			}
		})
	}
}
