package mgo

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/registrationproof"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/metadata"
)

type mongoRegistrationTokens map[string]int

func (f mongoRegistrationTokens) GetTokensWithoutError(context.Context, string, int) (map[string]int, error) {
	return f, nil
}

type mongoRegistrationNonces struct{}

func (mongoRegistrationNonces) Consume(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func authorizedMongoRegistration(t *testing.T, users []*sdkws.UserInfo) context.Context {
	t.Helper()
	oldSecret, oldIssuer, oldManagers := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-mongo-registration-", 3)
	config.Config.AdminDelegation.Issuer = "registration-mongo-fixture"
	config.Config.Manager.UserID = []string{"fixture-registration-service"}
	t.Cleanup(func() {
		config.Config.Secret = oldSecret
		config.Config.AdminDelegation.Issuer = oldIssuer
		config.Config.Manager.UserID = oldManagers
	})
	c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: "fixture-registration-service", PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Config.AdminDelegation.Issuer, ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: authverify.ServicePurpose}
	raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
	if e != nil {
		t.Fatal(e)
	}
	req := &user.UserRegisterReq{Secret: config.Config.Secret, Users: users}
	ctx, e := registrationproof.WithRPCProof(context.Background(), []byte(config.Config.Secret), raw, req, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	md := metadata.MD{}
	for _, key := range ctx.Value(constant.RpcCustomHeader).([]string) {
		md.Set(key, ctx.Value(key).([]string)...)
	}
	snapshot, e := registrationproof.Snapshot(users)
	if e != nil {
		t.Fatal(e)
	}
	s := registrationproof.Authorizer{Tokens: mongoRegistrationTokens{raw: constant.NormalToken}, Nonces: mongoRegistrationNonces{}, Secret: []byte(config.Config.Secret)}
	ctx, e = s.Authorize(metadata.NewIncomingContext(context.Background(), md), req, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	return ctx
}
func TestMongoRegistrationProofFullModelBatchBootstrapAndProfileUpdates(t *testing.T) {
	db := isolatedCommandMongo(t)
	store, e := NewUserMongo(db)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	public := `{"luoboRegistrationID":"` + uuid.NewString() + `","custom":"initial"}`
	admin := `{"luoboAdminProvisioningID":"` + uuid.NewString() + `"}`
	models := []*relation.UserModel{{UserID: "public", Ex: public}, {UserID: "admin", Ex: admin}}
	if e := store.Create(ctx, []*relation.UserModel{{UserID: "batch-prefix"}, models[0]}); e == nil {
		t.Fatal("untrusted full-model batch minted marker")
	}
	if count, _ := db.Collection("user").CountDocuments(ctx, bson.M{}); count != 0 {
		t.Fatal("invalid batch partially inserted users")
	}
	if store.CreateIfAbsent(ctx, models[1]) == nil {
		t.Fatal("bootstrap bypass minted marker")
	}
	trusted := authorizedMongoRegistration(t, []*sdkws.UserInfo{{UserID: "public", Ex: public}, {UserID: "admin", Ex: admin}})
	if e := mongotx.New(db.Client()).Transaction(trusted, func(ctx context.Context) error { return store.Create(ctx, models) }); e != nil {
		t.Fatal("exact service-proved model create rejected", e)
	}
	if store.Create(trusted, []*relation.UserModel{{UserID: "forged-owner", Ex: public}}) == nil {
		t.Fatal("grant reused for another full-model owner")
	}
	for _, owner := range []string{"public", "admin"} {
		original, e := store.Take(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		if e := store.CreateIfAbsent(ctx, &relation.UserModel{UserID: owner, Nickname: "must not replace"}); e != nil {
			t.Fatal(e)
		}
		for _, ex := range []string{original.Ex, `{"custom":"updated"}`, ""} {
			if e := store.UpdateByMap(ctx, owner, map[string]any{"ex": ex, "nickname": "ordinary"}); e != nil {
				t.Fatal("safe Ex update rejected", e)
			}
			got, e := store.Take(ctx, owner)
			if e != nil {
				t.Fatal(e)
			}
			a, _ := registrationproof.InitialMarkers(original.Ex)
			b, e := registrationproof.InitialMarkers(got.Ex)
			if e != nil || len(a) != len(b) {
				t.Fatal("proof missing after ordinary update")
			}
			for k, v := range a {
				if b[k] != v {
					t.Fatal("proof changed")
				}
			}
		}
		before, _ := store.Take(ctx, owner)
		for _, args := range []map[string]any{{"ex": `{"luoboRegistrationID":"` + uuid.NewString() + `"}`}, {"ex": `{"luoboAdminProvisioningID":"` + uuid.NewString() + `"}`}, {"ex": "raw string"}, {"ex": "[]"}, {"EX": ""}, {"ex.x": ""}, {"$unset": bson.M{"ex": 1}}, {"ex": nil}, {"user_id": "another"}, {"ex": `{"luoboRegiſtrationID":"` + uuid.NewString() + `"}`}} {
			if store.UpdateByMap(ctx, owner, args) == nil {
				t.Fatal("unsafe storage update accepted")
			}
			after, _ := store.Take(ctx, owner)
			if after.Ex != before.Ex || after.Nickname != before.Nickname || after.UserID != before.UserID {
				t.Fatal("rejected update changed profile")
			}
		}
	}
	if e := store.Create(ctx, []*relation.UserModel{{UserID: "legacy", Ex: "raw legacy"}, {UserID: "unmarked", Ex: `{"old":1}`}}); e != nil {
		t.Fatal(e)
	}
	for _, owner := range []string{"legacy", "unmarked"} {
		if store.UpdateByMap(ctx, owner, map[string]any{"ex": public}) == nil {
			t.Fatal("unmarked user forged proof")
		}
		if e := store.UpdateByMap(ctx, owner, map[string]any{"ex": `{"ordinary":"compatible"}`}); e != nil {
			t.Fatal(e)
		}
	}
	// Historical collections may omit Ex or store null instead of the typed
	// model's empty string; preserving legacy compatibility must not open proof
	// insertion through either representation.
	if _, e := db.Collection("user").InsertMany(ctx, []any{bson.M{"user_id": "missing-ex"}, bson.M{"user_id": "null-ex", "ex": nil}}); e != nil {
		t.Fatal(e)
	}
	for _, owner := range []string{"missing-ex", "null-ex"} {
		if store.UpdateByMap(ctx, owner, map[string]any{"ex": public}) == nil {
			t.Fatal("historical empty Ex forged proof")
		}
		if e := store.UpdateByMap(ctx, owner, map[string]any{"ex": `{"custom":"compatible"}`}); e != nil {
			t.Fatal("historical empty Ex CAS incompatible", e)
		}
	}
	t.Log("LUOBO_REGISTRATION_MARKER_MONGO_CREATE_UPDATE_PASS")
}

func TestMongoRegistrationProofCASDoesNotEraseConcurrentAuthoritativeValue(t *testing.T) {
	db := isolatedCommandMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var armed atomic.Bool
	ready, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	client, e := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("LUOBO_INTEGRATION_MONGO_URI")).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.DatabaseName == db.Name() && e.CommandName == "update" && armed.CompareAndSwap(true, false) {
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}))
	if e != nil {
		t.Fatal(e)
	}
	defer client.Disconnect(context.Background())
	store, e := NewUserMongo(client.Database(db.Name()))
	if e != nil {
		t.Fatal(e)
	}
	if e := store.Create(ctx, []*relation.UserModel{{UserID: "owner", Ex: ""}}); e != nil {
		t.Fatal(e)
	}
	armed.Store(true)
	result := make(chan error, 1)
	go func() {
		result <- store.UpdateByMap(ctx, "owner", map[string]any{"ex": `{"ordinary":"stale"}`, "nickname": "stale"})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("CAS update did not reach deterministic race boundary")
	}
	// A raw fixture write emulates a concurrently committed authoritative proof;
	// no public writer or production bypass is used to obtain this test state.
	proof := `{"luoboRegistrationID":"` + uuid.NewString() + `"}`
	if _, e := db.Collection("user").UpdateOne(ctx, bson.M{"user_id": "owner"}, bson.M{"$set": bson.M{"ex": proof, "nickname": "authoritative"}}); e != nil {
		t.Fatal(e)
	}
	unblock()
	if e := <-result; e == nil {
		t.Fatal("stale read overwrote concurrently committed proof")
	}
	got, e := store.Take(ctx, "owner")
	if e != nil || got.Ex != proof || got.Nickname != "authoritative" {
		t.Fatal("CAS lost the authoritative profile", e)
	}
	// Concurrent legitimate whole-value writes may retry, but each success must
	// retain the original marker and none may install a forged namespace.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.UpdateByMap(ctx, "owner", map[string]any{"ex": `{"custom":"new"}`})
			if store.UpdateByMap(ctx, "owner", map[string]any{"ex": `{"luoboAdminProvisioningID":"` + uuid.NewString() + `"}`}) == nil {
				t.Error("concurrent namespace substitution succeeded")
			}
		}()
	}
	wg.Wait()
	got, e = store.Take(ctx, "owner")
	m, e2 := registrationproof.InitialMarkers(got.Ex)
	if e != nil || e2 != nil || len(m) != 1 || m[registrationproof.PublicKey] == "" {
		t.Fatal("concurrent updates lost the proof")
	}
	t.Log("LUOBO_REGISTRATION_MARKER_MONGO_CAS_PASS")
}
