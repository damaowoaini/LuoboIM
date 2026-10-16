package chat

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	pb "github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"testing"
)

type profileDBFixture struct {
	database.ChatDatabaseInterface
	profile table.Attribute
	writes  int
}

func (d *profileDBFixture) TakeAttributeByUserID(context.Context, string) (*table.Attribute, error) {
	v := d.profile
	return &v, nil
}
func (d *profileDBFixture) UpdateUseInfo(ctx context.Context, id string, fields map[string]any) error {
	d.writes++
	if value, ok := fields["gender"]; ok {
		d.profile.Gender = value.(int32)
	}
	return nil
}
func TestProfileRPCPartialReturnsCanonicalUntouchedFieldsAndRejectsForeign(t *testing.T) {
	store := &profileDBFixture{profile: table.Attribute{UserID: "owner", Nickname: "original", FaceURL: "https://fixture.invalid/avatar", Gender: 1}}
	server := &chatSvr{Database: store}
	ctx := mctx.WithOpUserID(context.Background(), "owner", constant.NormalUser)
	resp, err := server.UpdateUserInfo(ctx, &pb.UpdateUserInfoReq{UserID: "owner", Gender: &wrapperspb.Int32Value{Value: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NickName != store.profile.Nickname || resp.FaceUrl != store.profile.FaceURL || resp.SyncPending || store.profile.Gender != 2 {
		t.Fatal("partial RPC response lost canonical fields")
	}
	for _, req := range []*pb.UpdateUserInfoReq{{UserID: "foreign", Gender: &wrapperspb.Int32Value{Value: 1}}, {UserID: "owner", Email: &wrapperspb.StringValue{Value: "test@example.com"}}, {UserID: "owner", Level: &wrapperspb.Int32Value{Value: 2}}, {UserID: "owner", RegisterType: &wrapperspb.Int32Value{Value: 2}}} {
		if _, err = server.UpdateUserInfo(ctx, req); err == nil {
			t.Fatal("protected identity/role update accepted")
		}
	}
	if store.writes != 1 {
		t.Fatal("denied profile mutation touched database")
	}
}
