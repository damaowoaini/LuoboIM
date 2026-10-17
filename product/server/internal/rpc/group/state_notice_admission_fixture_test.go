package group

import (
	"context"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"go.mongodb.org/mongo-driver/bson"
)

// Only the original muted-admin admission leaf needs the newly mandatory
// real notice transaction/runtime. Keep its ORIGINAL sentinel writer and
// writes==1 assertion: this fixture proves authorization reached UpdateGroup,
// then the strong transaction aborts. It is not a delivery-success mock.
type stateNoticeAdmissionDB struct {
	controller.GroupDatabase
	writer *stateAuthorityDB
}

func (d *stateNoticeAdmissionDB) UpdateGroup(ctx context.Context, gid string, fields map[string]any) error {
	return d.writer.UpdateGroup(ctx, gid, fields)
}

func stateNoticeAdmissionFixture(t *testing.T, writer *stateAuthorityDB, status int32, actor string) (context.Context, *groupServer) {
	t.Helper()
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	result, err := db.Collection("group").UpdateOne(ctx, bson.M{"group_id": "g"}, bson.M{"$set": bson.M{"status": status}})
	if err != nil || result.MatchedCount != 1 || result.ModifiedCount != 1 {
		t.Fatal("exact fresh fixture group status was not prepared", err)
	}
	service.db = &stateNoticeAdmissionDB{GroupDatabase: service.db, writer: writer}
	return statusActor(ctx, actor), service
}
