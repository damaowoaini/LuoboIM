package registrationproof

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
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type proofTokens struct {
	flags map[string]int
	fail  bool
}

func (f *proofTokens) GetTokensWithoutError(context.Context, string, int) (map[string]int, error) {
	if f.fail {
		return nil, errors.New("private token failure")
	}
	return f.flags, nil
}

type proofNonces struct {
	sync.Mutex
	seen map[string]bool
	fail bool
	ttl  time.Duration
}

func (n *proofNonces) Consume(_ context.Context, key string, ttl time.Duration) (bool, error) {
	n.Lock()
	defer n.Unlock()
	if n.fail {
		return false, errors.New("private Redis failure")
	}
	n.ttl = ttl
	if n.seen[key] {
		return false, nil
	}
	n.seen[key] = true
	return true, nil
}
func proofSetup(t *testing.T) (*Authorizer, *user.UserRegisterReq, string, func(string, string) string) {
	t.Helper()
	oldSecret, oldIssuer, oldManagers := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-registration-proof-", 3)
	config.Config.AdminDelegation.Issuer = "proof-tenant-A"
	config.Config.Manager.UserID = []string{"registration-service"}
	t.Cleanup(func() {
		config.Config.Secret = oldSecret
		config.Config.AdminDelegation.Issuer = oldIssuer
		config.Config.Manager.UserID = oldManagers
	})
	mint := func(purpose, issuer string) string {
		c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: "registration-service", PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: purpose}
		if purpose == authverify.DelegatedPurpose {
			c.Subject = "browser-admin"
			c.Audience = jwt.ClaimStrings{authverify.AdminAudience}
			c.Delegation = &authverify.Delegation{Version: 1, AdminID: "browser-admin", SessionHash: strings.Repeat("a", 64)}
		}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	raw := mint(authverify.ServicePurpose, config.Config.AdminDelegation.Issuer)
	s := &Authorizer{Secret: []byte(config.Config.Secret), Tokens: &proofTokens{flags: map[string]int{raw: constant.NormalToken}}, Nonces: &proofNonces{seen: map[string]bool{}}}
	req := &user.UserRegisterReq{Secret: config.Config.Secret, Users: []*sdkws.UserInfo{{UserID: "public-owner", Nickname: "public fixture", Ex: `{"luoboRegistrationID":"` + publicID + `"}`}, {UserID: "admin-owner", Nickname: "admin fixture", Ex: `{"luoboAdminProvisioningID":"` + adminID + `"}`}}}
	return s, req, raw, mint
}
func proofIncoming(ctx context.Context) context.Context {
	md := metadata.MD{}
	for _, key := range ctx.Value(constant.RpcCustomHeader).([]string) {
		md.Set(key, ctx.Value(key).([]string)...)
	}
	md.Set(constant.OpUserID, "forged-manager")
	return metadata.NewIncomingContext(context.Background(), md)
}
func TestInitialRegistrationRPCRequiresCurrentPurposeAndExactRequestProof(t *testing.T) {
	s, req, raw, mint := proofSetup(t)
	snap, _ := Snapshot(req.Users)
	sign := func(token string, at time.Time) context.Context {
		ctx, e := WithRPCProof(context.Background(), s.Secret, token, req, at)
		if e != nil {
			t.Fatal(e)
		}
		return proofIncoming(ctx)
	}
	proof := sign(raw, time.Now())
	ctx, e := s.Authorize(proof, req, snap)
	if e != nil {
		t.Fatal("valid proof rejected", e)
	}
	for _, u := range req.Users {
		if CheckCreate(ctx, u.UserID, u.Ex) != nil {
			t.Fatal("authorized original batch not granted")
		}
	}
	if CheckCreate(ctx, "another-owner", req.Users[0].Ex) == nil {
		t.Fatal("proof grant usable for another owner")
	}
	if _, e := s.Authorize(proof, req, snap); e == nil {
		t.Fatal("replayed creation proof accepted")
	}
	if s.Nonces.(*proofNonces).ttl < 11*time.Minute {
		t.Fatal("nonce expired before signature-skew replay window")
	}
	if _, e := s.Authorize(context.WithValue(context.Background(), constant.OpUserID, "registration-service"), req, snap); e == nil {
		t.Fatal("legacy opuser metadata authorized marker")
	}
	mutated := proto.Clone(req).(*user.UserRegisterReq)
	mutated.Users[0].Nickname = "different signed body"
	ms, _ := Snapshot(mutated.Users)
	if _, e := s.Authorize(sign(raw, time.Now()), mutated, ms); e == nil {
		t.Fatal("signed body substitution accepted")
	}
	mutated.Users[0].UserID = "substituted"
	ms, _ = Snapshot(mutated.Users)
	if _, e := s.Authorize(sign(raw, time.Now()), mutated, ms); e == nil {
		t.Fatal("signed principal substitution accepted")
	}
	for _, token := range []string{mint(authverify.DelegatedPurpose, config.Config.AdminDelegation.Issuer), mint("", config.Config.AdminDelegation.Issuer), mint(authverify.ServicePurpose, "proof-tenant-B")} {
		s.Tokens.(*proofTokens).flags[token] = constant.NormalToken
		if _, e := s.Authorize(sign(token, time.Now()), req, snap); e == nil {
			t.Fatal("wrong purpose/issuer accepted even with live token and valid MAC")
		}
	}
	if _, e := s.Authorize(sign(raw, time.Now().Add(-301*time.Second)), req, snap); e == nil {
		t.Fatal("expired proof accepted")
	}
	body, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	h, e := serviceauth.Sign(s.Secret, serviceauth.Internal, http.MethodPost, "/user/user_register", raw, body, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	md := metadata.Pairs(rpcToken, raw)
	for _, key := range rpcProofHeaders {
		md.Set(strings.ToLower(key), h.Get(key))
	}
	if _, e := s.Authorize(metadata.NewIncomingContext(context.Background(), md), req, snap); e == nil {
		t.Fatal("HTTP MAC reused for GRPC")
	}
	valid := sign(raw, time.Now())
	dup, _ := metadata.FromIncomingContext(valid)
	dup = dup.Copy()
	dup.Append(rpcToken, raw)
	if _, e := s.Authorize(metadata.NewIncomingContext(context.Background(), dup), req, snap); e == nil {
		t.Fatal("ambiguous duplicated credential accepted")
	}
	s.Tokens.(*proofTokens).flags[raw] = constant.KickedToken
	if _, e := s.Authorize(sign(raw, time.Now()), req, snap); e == nil {
		t.Fatal("revoked service session accepted")
	}
	s.Tokens.(*proofTokens).flags[raw] = constant.NormalToken
	s.Tokens.(*proofTokens).fail = true
	if _, e := s.Authorize(sign(raw, time.Now()), req, snap); e == nil || strings.Contains(e.Error(), "private token") {
		t.Fatal("token store outage not safely rejected")
	}
	s.Tokens.(*proofTokens).fail = false
	s.Nonces.(*proofNonces).fail = true
	if _, e := s.Authorize(sign(raw, time.Now()), req, snap); e == nil || strings.Contains(e.Error(), "private Redis") {
		t.Fatal("nonce outage not safely rejected")
	}
	legacy := &user.UserRegisterReq{Users: []*sdkws.UserInfo{{UserID: "legacy", Ex: "raw Ex"}}}
	ls, _ := Snapshot(legacy.Users)
	if _, e := (*Authorizer)(nil).Authorize(context.Background(), legacy, ls); e != nil {
		t.Fatal("marker-free legacy registration changed")
	}
}

type proofRPCServer struct {
	user.UnimplementedUserServer
	auth *Authorizer
}

func (s *proofRPCServer) UserRegister(ctx context.Context, req *user.UserRegisterReq) (*user.UserRegisterResp, error) {
	snap, e := Snapshot(req.Users)
	if e != nil {
		return nil, e
	}
	ctx, e = s.auth.Authorize(ctx, req, snap)
	if e != nil {
		return nil, e
	}
	for _, u := range req.Users {
		if e := CheckCreate(ctx, u.UserID, u.Ex); e != nil {
			return nil, e
		}
	}
	return &user.UserRegisterResp{}, nil
}
func TestRegistrationProofSurvivesProductionRPCMetadataInterceptors(t *testing.T) {
	s, req, raw, _ := proofSetup(t)
	listener := bufconn.Listen(128 * 1024)
	server := grpc.NewServer(rpcmiddleware.GrpcServer())
	user.RegisterUserServer(server, &proofRPCServer{auth: s})
	if _, registered := server.GetServiceInfo()[strings.TrimSuffix(strings.TrimPrefix(RPCMethod, "/"), "/UserRegister")]; !registered {
		t.Fatal("registration proof method does not match registered generated RPC")
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, e := grpc.DialContext(ctx, "passthrough:///registration-proof", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	ctx = context.WithValue(ctx, constant.OperationID, "synthetic-registration-"+uuid.NewString())
	signed, e := WithRPCProof(ctx, s.Secret, raw, req, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	client := user.NewUserClient(conn)
	if _, e := client.UserRegister(signed, req); e != nil {
		t.Fatal("production interceptor dropped valid creation proof", e)
	}
	if _, e := client.UserRegister(signed, req); e == nil {
		t.Fatal("production RPC allowed nonce replay")
	}
	if _, e := client.UserRegister(ctx, req); e == nil {
		t.Fatal("production RPC accepted missing proof")
	}
}
