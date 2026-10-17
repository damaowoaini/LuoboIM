package third

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/third"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"strings"
	"testing"
	"time"
)

type formFixture struct {
	controller.S3Database
	key    string
	reads  int
	writes int
	size   int64
}

func (f *formFixture) Engine() string { return "fixture" }
func (f *formFixture) FormData(ctx context.Context, name string, size int64, contentType string, duration time.Duration) (*s3.FormData, error) {
	f.key = name
	f.size = size
	return &s3.FormData{URL: "https://fixture.invalid/upload", File: "file", Expires: time.Now().Add(duration), SuccessCodes: []int{204}}, nil
}
func (f *formFixture) StatObject(ctx context.Context, name string) (*s3.ObjectInfo, error) {
	f.reads++
	if name != f.key {
		panic("read unexpected source key")
	}
	return &s3.ObjectInfo{Key: name, Size: f.size, ETag: "synthetic-etag"}, nil
}
func (f *formFixture) SetObject(ctx context.Context, info *relation.ObjectModel) error {
	if info.Key != f.key || info.UserID != "upload-owner" {
		panic("unexpected alias owner/key")
	}
	f.writes++
	return nil
}
func TestFormUploadReceiptEnforcedBeforeProviderReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mc, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"))
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Disconnect(context.Background())
	if err = mc.Ping(ctx, nil); err != nil {
		t.Skip("isolated Mongo unavailable")
	}
	db := mc.Database("luobo_form_rpc_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer db.Drop(context.Background())
	store, err := intent.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	provider := &formFixture{}
	server := &thirdServer{s3dataBase: provider, intents: store, apiURL: "https://fixture.invalid/api/object/"}
	owner := context.WithValue(ctx, constant.OpUserID, "upload-owner")
	other := context.WithValue(ctx, constant.OpUserID, "other-user")
	res, err := server.InitiateFormData(owner, &pb.InitiateFormDataReq{Name: "upload-owner/synthetic.txt", Size: 12, ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Id, intent.Prefix) {
		t.Fatal("form ID not opaque")
	}
	if _, err = server.CompleteFormData(other, &pb.CompleteFormDataReq{Id: res.Id}); err == nil {
		t.Fatal("foreign receipt accepted")
	}
	if _, err = server.CompleteFormData(owner, &pb.CompleteFormDataReq{Id: "eyJuYW1lIjoidXBsb2FkLW93bmVyL2YiLCJrZXkiOiJ2aWN0aW0ifQ"}); err == nil {
		t.Fatal("unsigned form ID accepted")
	}
	if provider.reads != 0 || provider.writes != 0 {
		t.Fatal("unauthorized request reached provider")
	}
	server.intents, err = intent.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.CompleteFormData(owner, &pb.CompleteFormDataReq{Id: res.Id}); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 {
		t.Fatal("owner completion not persisted")
	}
	t.Log("LUOBO_FORM_RPC_RECEIPT_AUTHORIZATION_PASS")
}
