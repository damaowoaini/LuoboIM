package office

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	pb "github.com/OpenIMSDK/chat/pkg/proto/office"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"reflect"
	"testing"
	"time"
)

type momentReadStub struct {
	database.OfficeDatabaseInterface
	moment *table.WorkMoment
}

func TestMentionCannotChangeVisibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		permission int32
		want       []string
	}{
		{"public", constant.WorkMomentPublic, []string{"bob", "carol"}},
		{"private", constant.WorkMomentPrivate, []string{}},
		{"included", constant.WorkMomentPermissionCanSee, []string{"bob"}},
		{"excluded", constant.WorkMomentPermissionCantSee, []string{"carol"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := visibleMomentMentions(tc.permission, "alice", []string{"bob"}, []string{"alice", "bob", "carol", "bob"})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mentions changed visible audience: %v", got)
			}
		})
	}
}

func (s momentReadStub) GetWorkMomentByID(context.Context, string) (*table.WorkMoment, error) {
	return s.moment, nil
}

type momentUsersStub struct{ publicUserReader }

func (momentUsersStub) MapUserPublicInfo(_ context.Context, ids []string) (map[string]*common.UserPublicInfo, error) {
	out := map[string]*common.UserPublicInfo{}
	for _, id := range ids {
		out[id] = &common.UserPublicInfo{UserID: id, Nickname: id}
	}
	return out, nil
}

func TestRepeatedLikeAndUnlikeStillReturnRenderableMoment(t *testing.T) {
	for _, liked := range []bool{false, true} {
		moment := &table.WorkMoment{WorkMomentID: primitive.NewObjectID(), UserID: "alice", Content: &table.WorkMomentContent{Text: "original"}, CreateTime: time.Now()}
		if liked {
			moment.LikeUsers = []*table.LikeUser{{UserID: "alice", CreateTime: time.Now()}}
		}
		server := &officeServer{db: momentReadStub{moment: moment}, user: momentUsersStub{}}
		ctx := mctx.WithOpUserID(context.Background(), "alice", constant.NormalUser)
		response, err := server.LikeOneWorkMoment(ctx, &pb.LikeOneWorkMomentReq{WorkMomentID: moment.WorkMomentID.Hex(), Like: liked})
		if err != nil || response == nil || response.WorkMoment == nil {
			t.Fatalf("repeated like=%v returned no moment: %v", liked, err)
		}
		if response.WorkMoment.UserID != "alice" || response.WorkMoment.Content.Text != "original" || len(response.WorkMoment.LikeUsers) != len(moment.LikeUsers) {
			t.Fatal("idempotent mutation lost existing content or state")
		}
	}
}
