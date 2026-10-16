package objectverify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type tokenFixture struct {
	flags map[string]int
	fail  bool
}

func (f *tokenFixture) GetTokensWithoutError(context.Context, string, int) (map[string]int, error) {
	if f.fail {
		return nil, errors.New("private token dependency unavailable")
	}
	return f.flags, nil
}

type nonceFixture struct {
	mu   sync.Mutex
	seen map[string]bool
	fail bool
	ttl  time.Duration
}

func (f *nonceFixture) Consume(_ context.Context, key string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return false, errors.New("private nonce dependency unavailable")
	}
	f.ttl = ttl
	if f.seen[key] {
		return false, nil
	}
	f.seen[key] = true
	return true, nil
}
func rpcSetup(t *testing.T) (*RPCServer, *pb.VerifyRequest, string, func(string, string) string) {
	t.Helper()
	secret, issuer, manager := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-rpc-evidence-", 3)
	config.Config.AdminDelegation.Issuer = "evidence-tenant-A"
	config.Config.Manager.UserID = []string{"evidence-service"}
	t.Cleanup(func() {
		config.Config.Secret = secret
		config.Config.AdminDelegation.Issuer = issuer
		config.Config.Manager.UserID = manager
	})
	mint := func(purpose, issuer string) string {
		c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: "evidence-service", PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: purpose}
		if purpose == authverify.DelegatedPurpose {
			c.Subject = "actor"
			c.Audience = jwt.ClaimStrings{authverify.AdminAudience}
			c.Delegation = &authverify.Delegation{Version: 1, AdminID: "actor", SessionHash: strings.Repeat("a", 64)}
		}
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := mint(authverify.ServicePurpose, config.Config.AdminDelegation.Issuer)
	name := "owner/evidence.png"
	s := &RPCServer{Verifier: &Verifier{Objects: &objectFixture{objects: map[string]*relation.ObjectModel{name: completed(name, "owner")}}, Engine: "fixture", APIBase: "https://media.fixture.invalid/api/object/"}, Tokens: &tokenFixture{flags: map[string]int{raw: constant.NormalToken}}, Nonces: &nonceFixture{seen: map[string]bool{}}, Secret: []byte(config.Config.Secret)}
	return s, &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{s.Verifier.APIBase + name}}, raw, mint
}
func incomingProof(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	md := metadata.MD{}
	for _, key := range ctx.Value(constant.RpcCustomHeader).([]string) {
		md.Set(key, ctx.Value(key).([]string)...)
	}
	// A forged legacy opuser is deliberately irrelevant to this service.
	md.Set(constant.OpUserID, "forged-manager")
	return metadata.NewIncomingContext(context.Background(), md)
}
func TestRPCProofRejectsReplayBodyIdentityIssuerClassAndDependencyFailures(t *testing.T) {
	s, req, raw, mint := rpcSetup(t)
	sign := func(token string, at time.Time) context.Context {
		ctx, e := WithRPCProof(context.Background(), s.Secret, token, req, at)
		if e != nil {
			t.Fatal(e)
		}
		return incomingProof(t, ctx)
	}
	proof := sign(raw, time.Now())
	if r, e := s.Verify(proof, req); e != nil || r == nil || !r.Valid {
		t.Fatal("valid private proof rejected")
	}
	if _, e := s.Verify(proof, req); e == nil {
		t.Fatal("RPC nonce replay accepted")
	}
	if s.Nonces.(*nonceFixture).ttl < 11*time.Minute {
		t.Fatal("RPC nonce lifetime shorter than shared skew-safe bound")
	}
	if _, e := s.Verify(context.Background(), req); e == nil {
		t.Fatal("unsigned private request accepted")
	}
	altered := &pb.VerifyRequest{OwnerUserId: "peer", Urls: req.Urls}
	if _, e := s.Verify(sign(raw, time.Now()), altered); e == nil {
		t.Fatal("signed owner substitution accepted")
	}
	for _, token := range []string{mint(authverify.DelegatedPurpose, config.Config.AdminDelegation.Issuer), mint("", config.Config.AdminDelegation.Issuer), mint(authverify.ServicePurpose, "evidence-tenant-B")} {
		if _, e := s.Verify(sign(token, time.Now()), req); e == nil {
			t.Fatal("wrong class/issuer accepted with valid MAC")
		}
	}
	if _, e := s.Verify(sign(raw, time.Now().Add(-301*time.Second)), req); e == nil {
		t.Fatal("expired request proof accepted")
	}
	// A valid proof from the HTTP hop is not a proof for this private RPC.
	wrongTransport, err := serviceauth.Sign(s.Secret, serviceauth.Internal, http.MethodPost, HTTPPath, raw, []byte(`{"ownerUserID":"owner"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	md := metadata.Pairs(rpcTokenHeader, raw)
	for _, key := range proofHeaders {
		md.Set(strings.ToLower(key), wrongTransport.Get(key))
	}
	if _, e := s.Verify(metadata.NewIncomingContext(context.Background(), md), req); e == nil {
		t.Fatal("HTTP proof reused as private RPC proof")
	}
	validProof := sign(raw, time.Now())
	md, _ = metadata.FromIncomingContext(validProof)
	md = md.Copy()
	md.Append(rpcTokenHeader, raw)
	if _, e := s.Verify(metadata.NewIncomingContext(context.Background(), md), req); e == nil {
		t.Fatal("ambiguous duplicate RPC credential accepted")
	}
	s.Tokens.(*tokenFixture).flags[raw] = constant.KickedToken
	if _, e := s.Verify(sign(raw, time.Now()), req); e == nil {
		t.Fatal("revoked service token accepted")
	}
	s.Tokens.(*tokenFixture).flags[raw] = constant.NormalToken
	s.Tokens.(*tokenFixture).fail = true
	if _, e := s.Verify(sign(raw, time.Now()), req); e == nil || strings.Contains(e.Error(), "private token") {
		t.Fatal("token dependency did not fail closed safely")
	}
	s.Tokens.(*tokenFixture).fail = false
	s.Nonces.(*nonceFixture).fail = true
	if _, e := s.Verify(sign(raw, time.Now()), req); e == nil || strings.Contains(e.Error(), "private nonce") {
		t.Fatal("nonce dependency did not fail closed safely")
	}
}
func TestPrivateRPCUsesStandardGRPCAndProductionMetadataInterceptors(t *testing.T) {
	s, req, raw, _ := rpcSetup(t)
	if RPCMethod != pb.EvidenceVerifier_Verify_FullMethodName {
		t.Fatal("proof path differs from generated service")
	}
	listener := bufconn.Listen(64 * 1024)
	server := grpc.NewServer(rpcmiddleware.GrpcServer())
	pb.RegisterEvidenceVerifierServer(server, s)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///evidence-bufconn", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx = context.WithValue(ctx, constant.OperationID, "synthetic-evidence-"+uuid.NewString())
	proof, err := WithRPCProof(ctx, s.Secret, raw, req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := pb.NewEvidenceVerifierClient(conn)
	if r, e := client.Verify(proof, req); e != nil || r == nil || !r.Valid {
		t.Fatal("standard private gRPC valid request failed")
	}
	if _, e := client.Verify(proof, req); e == nil {
		t.Fatal("standard private gRPC replay accepted")
	}
	if _, e := client.Verify(ctx, req); e == nil {
		t.Fatal("opuser-only private gRPC accepted")
	}
}
