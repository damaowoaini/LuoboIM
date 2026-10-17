package third

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/third"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func formFixtureMongoURIAllowed(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "mongodb" || u.Host != "127.0.0.1:37017" || u.User != nil || u.Path != "/" || u.RawPath != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	if len(query) == 0 {
		return true
	}
	values := query["replicaSet"]
	return len(query) == 1 && len(values) == 1 && values[0] == "luoboIntegration"
}

func TestFormUploadMongoFixtureURIGuard(t *testing.T) {
	for _, uri := range []string{"mongodb://127.0.0.1:37017/", "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"} {
		if !formFixtureMongoURIAllowed(uri) {
			t.Fatal("task fixture rejected")
		}
	}
	for _, uri := range []string{"mongodb+srv://127.0.0.1/", "mongodb://127.0.0.1:37017@external.invalid/", "mongodb://127.0.0.1:37017,external.invalid:27017/", "mongodb://external.invalid:37017/", "mongodb://127.0.0.1:27017/", "mongodb://user:password@127.0.0.1:37017/", "mongodb://127.0.0.1:37017/luobo_stack", "mongodb://127.0.0.1:37017/?proxyHost=external.invalid", "mongodb://127.0.0.1:37017/?replicaSet=other", "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&replicaSet=luoboIntegration", "mongodb://127.0.0.1:37017/#fragment"} {
		if formFixtureMongoURIAllowed(uri) {
			t.Fatal("nonfixture URI accepted")
		}
	}
}

func TestFormUploadReservedAbsoluteRejectedBeforeCapabilityOrProvider(t *testing.T) {
	previous := config.Config.Manager.UserID
	config.Config.Manager.UserID = []string{"synthetic-storage-manager"}
	defer func() { config.Config.Manager.UserID = previous }()
	p := &formFixture{}
	s := &thirdServer{s3dataBase: p} // No receipt repository: rejects must precede it.
	ctx := context.WithValue(context.Background(), constant.OpUserID, "synthetic-storage-manager")
	for _, name := range []string{"openim/data/owner-v2/scope/key", "openim/temp/key", "public/../openim/temp/key", "%6fpenim/data/key", "public%2f..%2fopenim/data/key"} {
		if _, err := s.InitiateFormData(ctx, &pb.InitiateFormDataReq{Name: name, Size: 12, Absolute: true}); !errs.ErrNoPermission.Is(err) {
			t.Fatal("reserved manager target not rejected", err)
		}
	}
	if p.key != "" || p.writes != 0 || p.reads != 0 {
		t.Fatal("reserved target reached provider")
	}
	if _, err := s.InitiateFormData(context.Background(), &pb.InitiateFormDataReq{Name: "legacy-assets/logo.png", Size: 12, Absolute: true}); err == nil {
		t.Fatal("unauthenticated form upload accepted")
	}
}

type formOwnedFixture struct {
	*formFixture
	owner string
}

func (f *formOwnedFixture) SetObject(_ context.Context, o *relation.ObjectModel) error {
	if o.UserID != f.owner || o.Key != f.key || o.Size != f.size {
		return errs.ErrNoPermission
	}
	f.writes++
	return nil
}

func TestFormUploadCanonicalManagerAbsoluteAndOrdinaryOwnerRemainCompatible(t *testing.T) {
	uri := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("explicit isolated LUOBO_STORAGE_TEST_MONGO_URI required")
	}
	if !formFixtureMongoURIAllowed(uri) {
		t.Fatal("only task-private loopback Mongo fixture permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal("cannot initialize task-private Mongo client")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Disconnect(cleanup); err != nil {
			t.Error("task-private Mongo disconnect failed")
		}
	})
	if err = client.Ping(ctx, nil); err != nil {
		t.Fatal("cannot connect to task-private Mongo fixture")
	}
	db := client.Database("luobo_form_reserved_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.Drop(cleanup); err != nil {
			t.Error("isolated form upload test database cleanup failed")
		}
	})
	receipts, err := intent.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	previous := config.Config.Manager.UserID
	config.Config.Manager.UserID = []string{"synthetic-storage-manager"}
	defer func() { config.Config.Manager.UserID = previous }()
	for _, tc := range []struct {
		owner, name string
		absolute    bool
		exactKey    bool
	}{{"synthetic-storage-manager", "legacy-assets/logo.png", true, true}, {"normal-owner", "normal-owner/logo.png", true, false}, {"normal-owner", "normal-owner/ordinary.png", false, false}} {
		actor := context.WithValue(ctx, constant.OpUserID, tc.owner)
		p := &formOwnedFixture{formFixture: &formFixture{}, owner: tc.owner}
		s := &thirdServer{s3dataBase: p, intents: receipts, apiURL: "https://fixture.invalid/api/object/"}
		res, err := s.InitiateFormData(actor, &pb.InitiateFormDataReq{Name: tc.name, Size: 12, Absolute: tc.absolute, ContentType: "image/png"})
		if err != nil {
			t.Fatal(err)
		}
		if tc.exactKey {
			if p.key != tc.name {
				t.Fatal("canonical legacy manager key changed")
			}
		} else if !strings.HasPrefix(p.key, "openim/direct/") || p.key == tc.name {
			t.Fatal("ordinary actor selected raw provider key")
		}
		if _, err = s.CompleteFormData(actor, &pb.CompleteFormDataReq{Id: res.Id}); err != nil || p.writes != 1 {
			t.Fatal("valid own form upload broke", err)
		}
	}
}
