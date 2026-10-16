package objectverify

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// Test-only completed alias fixtures, not an upload or image-byte validation
// claim. Uses a random database and exact Redis fields/nonces, never FLUSHDB.
func TestCompletedMetadataRealMongoAndPrivateRPCRealRedisReplay(t *testing.T) {
	s, _, raw, _ := rpcSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mc, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration").SetServerSelectionTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mc.Disconnect(context.Background()) })
	if mc.Ping(ctx, nil) != nil {
		t.Skip("private task Mongo replica set unavailable")
	}
	db := mc.Database("luobo_evidence_verify_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		if err := db.Drop(context.Background()); err != nil {
			t.Error("synthetic evidence database cleanup failed")
		}
	})
	objects, err := mgo.NewS3Mongo(db)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if rdb.Ping(ctx).Err() != nil {
		rdb.Close()
		t.Skip("private task Redis unavailable")
	}
	t.Cleanup(func() { rdb.Close() })
	tokens := cache.NewMsgCacheModel(rdb)
	if err = tokens.AddTokenFlag(ctx, "evidence-service", 10, raw, constant.NormalToken); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rdb.HDel(context.Background(), "UID_PID_TOKEN_STATUS:evidence-service:"+constant.PlatformIDToName(10), raw)
	})
	s.Verifier.Objects = objects
	s.Tokens = tokens
	s.Nonces = RedisNonces{Client: rdb}
	for _, obj := range []*relation.ObjectModel{completed("owner/ok.png", "owner"), completed("peer/foreign.png", "peer"), completed("owner/legacy.png", "")} {
		if obj.UserID == "" {
			// Model a pre-upgrade row directly in this random test database.
			// The protected writer must never create/adopt an unknown owner.
			_, err = db.Collection("s3").InsertOne(ctx, obj)
		} else {
			err = objects.SetObject(ctx, obj)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	proofFor := func(request *pb.VerifyRequest) context.Context {
		t.Helper()
		signed, err := WithRPCProof(context.Background(), s.Secret, raw, request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		incoming := incomingProof(t, signed)
		// Derive only our exact random nonce for cleanup, never remove other keys.
		md, _ := metadata.FromIncomingContext(incoming)
		h := http.Header{}
		for _, k := range proofHeaders {
			h.Set(k, md.Get(strings.ToLower(k))[0])
		}
		body, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
		key, err := serviceauth.Verify(s.Secret, serviceauth.Internal, "GRPC", RPCMethod, raw, body, h, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rdb.Del(context.Background(), key) })
		return incoming
	}
	for _, tc := range []struct {
		name, owner string
		want        bool
	}{
		{"owner/ok.png", "owner", true},
		{"peer/foreign.png", "owner", false},
		{"owner/legacy.png", "owner", false},
		{"owner/initialized-not-completed.png", "owner", false},
	} {
		req := &pb.VerifyRequest{OwnerUserId: tc.owner, Urls: []string{s.Verifier.APIBase + tc.name}}
		result, err := s.Verify(proofFor(req), req)
		if tc.want {
			if err != nil || result == nil || !result.Valid {
				t.Fatal("completed Mongo owner metadata denied")
			}
		} else if err == nil || !errs.ErrArgs.Is(err) {
			t.Fatal("foreign/missing metadata was accepted or leaked")
		}
	}
	req := &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{s.Verifier.APIBase + "owner/ok.png"}}
	incoming := proofFor(req)
	var wg sync.WaitGroup
	results := make(chan bool, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.Verify(incoming, req); results <- e == nil && r != nil && r.Valid }()
	}
	wg.Wait()
	close(results)
	success := 0
	for passed := range results {
		if passed {
			success++
		}
	}
	if success != 1 {
		t.Fatal("real Redis did not atomically consume single-use private RPC nonce")
	}
	if err = tokens.AddTokenFlag(ctx, "evidence-service", 10, raw, constant.KickedToken); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(proofFor(req), req); err == nil {
		t.Fatal("revoked real cached service token accepted")
	}
	t.Log("LUOBO_EVIDENCE_COMPLETED_MONGO_OWNER_AND_RPC_REDIS_REPLAY_PASS")
}
