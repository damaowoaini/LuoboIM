package office

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	pb "github.com/OpenIMSDK/chat/pkg/proto/office"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type tagDBStub struct {
	database.OfficeDatabaseInterface
	tags   []*table.Tag
	logged bool
}

func (d *tagDBStub) FindTag(context.Context, []string) ([]*table.Tag, error) { return d.tags, nil }
func (d *tagDBStub) CreateTagSendLog(context.Context, *table.TagSendLog) error {
	d.logged = true
	return nil
}

type tagUsersStub struct{ publicUserReader }

func (tagUsersStub) FindUserPublicInfo(_ context.Context, ids []string) ([]*common.UserPublicInfo, error) {
	users := []*common.UserPublicInfo{}
	for _, id := range ids {
		users = append(users, &common.UserPublicInfo{UserID: id})
	}
	return users, nil
}

func TestTagSecuritySendRequiresEveryTagOwner(t *testing.T) {
	for _, tc := range []struct {
		name, owner string
		role        int
		allowed     bool
	}{
		{"own tag", "u1", constant.NormalUser, true},
		{"other user tag", "u2", constant.NormalUser, false},
		{"authorized admin", "u2", constant.AdminUser, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := primitive.NewObjectID()
			db := &tagDBStub{tags: []*table.Tag{{TagID: id, UserID: tc.owner, UserIDs: []string{"recipient"}}}}
			s := &officeServer{db: db, user: tagUsersStub{}}
			ctx := mctx.WithOpUserID(context.Background(), "u1", tc.role)
			resp, err := s.SendMsg2Tag(ctx, &pb.SendMsg2TagReq{TagIDs: []string{id.Hex()}})
			if (err == nil) != tc.allowed || db.logged != tc.allowed {
				t.Fatalf("allowed=%v logged=%v err=%v", tc.allowed, db.logged, err)
			}
			if tc.allowed && (len(resp.RecvUserIDs) != 1 || resp.RecvUserIDs[0] != "recipient") {
				t.Fatal("authorized tag recipients changed")
			}
		})
	}
}

func TestTagSecurityAnonymousAndMissingTagsFailBeforeSending(t *testing.T) {
	s := &officeServer{db: &tagDBStub{}}
	if _, err := s.SendMsg2Tag(context.Background(), &pb.SendMsg2TagReq{}); err == nil {
		t.Fatal("anonymous send accepted")
	}
	ctx := mctx.WithOpUserID(context.Background(), "u1", constant.NormalUser)
	if _, err := s.SendMsg2Tag(ctx, &pb.SendMsg2TagReq{TagIDs: []string{primitive.NewObjectID().Hex()}}); err == nil {
		t.Fatal("missing tag silently ignored")
	}
}
