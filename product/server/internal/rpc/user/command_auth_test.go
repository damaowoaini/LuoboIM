package user

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
)

type commandDBStub struct {
	controller.UserDatabase
	deleted string
}

func (d *commandDBStub) DeleteUserCommand(_ context.Context, userID string, _ int32, _ string) error {
	d.deleted = userID
	return nil
}

func TestDeleteUserCommandRequiresOwner(t *testing.T) {
	for _, tc := range []struct {
		name, actor, owner string
		allowed            bool
	}{
		{"owner", "u1", "u1", true},
		{"other account", "u1", "u2", false},
		{"anonymous", "", "u2", false},
		{"missing identity and owner", "", "", false},
		{"missing owner", "u1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &commandDBStub{}
			s := &userServer{UserDatabase: db}
			ctx := context.WithValue(context.Background(), constant.OpUserID, tc.actor)
			_, err := s.ProcessUserCommandDelete(ctx, &pbuser.ProcessUserCommandDeleteReq{UserID: tc.owner, Uuid: "command"})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed = %v, err = %v", tc.allowed, err)
			}
			if !tc.allowed && db.deleted != "" {
				t.Fatal("unauthorized delete reached database")
			}
			if tc.allowed && db.deleted != tc.owner {
				t.Fatal("owner delete did not reach database")
			}
		})
	}
}
