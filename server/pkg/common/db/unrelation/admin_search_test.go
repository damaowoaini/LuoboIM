package unrelation

import (
	"context"
	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"strings"
	"testing"
	"time"
)

func TestAdminSearchPagedLiteralAndGlobalDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, e := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration").SetServerSelectionTimeout(time.Second))
	if e != nil {
		t.Skip("private Mongo unavailable")
	}
	defer client.Disconnect(context.Background())
	if client.Ping(ctx, nil) != nil {
		t.Skip("private Mongo unavailable")
	}
	db := client.Database("luobo_admin_search_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer db.Drop(context.Background())
	col := db.Collection("msg")
	var items []*table.MsgInfoModel
	for i := 0; i < 5; i++ {
		items = append(items, &table.MsgInfoModel{Msg: &table.MsgDataModel{SendID: "actor.literal", RecvID: "peer", Seq: int64(i + 1), SendTime: int64(i + 1), SessionType: 1, ContentType: 101, Content: "synthetic"}})
	}
	items = append(items, &table.MsgInfoModel{Msg: &table.MsgDataModel{SendID: "actor.literal", RecvID: "peer", Seq: 6, SendTime: 6, ContentType: 101, SessionType: 1, Content: "must-not-return"}, GlobalDeleted: true}, &table.MsgInfoModel{Msg: &table.MsgDataModel{SendID: "actorXliteral", RecvID: "peer", Seq: 7, SendTime: 7, ContentType: 101, SessionType: 1}})
	if _, e = col.InsertOne(ctx, &table.MsgDocModel{DocID: "si_fixture:0", Msg: items}); e != nil {
		t.Fatal(e)
	}
	if _, e = col.InsertOne(ctx, &table.MsgDocModel{DocID: "sg_group.literal:0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "group-sender", GroupID: "group.literal", RecvID: "", SessionType: 3, Seq: 1, SendTime: 10, ContentType: 101}}}}); e != nil {
		t.Fatal(e)
	}
	driver := &MsgMongoDriver{MsgCollection: col}
	req := &msg.SearchMessageReq{SendID: "actor.literal", Pagination: &sdkws.RequestPagination{PageNumber: 2, ShowNumber: 2}}
	total, page, e := driver.SearchMessage(ctx, req)
	if e != nil || total != 5 || len(page) != 2 || page[0].Msg.Seq != 3 || page[1].Msg.Seq != 2 {
		t.Fatal("page/count/literal/global deletion mismatch", total, len(page), e)
	}
	req.Pagination.PageNumber = 4
	total, page, e = driver.SearchMessage(ctx, req)
	if e != nil || total != 5 || len(page) != 0 {
		t.Fatal("empty page lost total", e)
	}
	req.SendID = ".*"
	req.Pagination.PageNumber = 1
	total, _, e = driver.SearchMessage(ctx, req)
	if e != nil || total != 0 {
		t.Fatal("IDs were treated as regular expressions")
	}
	req.SendID = ""
	req.RecvID = "group.literal"
	req.SessionType = 3
	total, page, e = driver.SearchMessage(ctx, req)
	if e != nil || total != 1 || len(page) != 1 || page[0].Msg.GroupID != "group.literal" {
		t.Fatal("group receiver filter did not use groupID", e)
	}
	req.Pagination = nil
	if _, _, e = driver.SearchMessage(ctx, req); e == nil {
		t.Fatal("missing pagination accepted")
	}
}
