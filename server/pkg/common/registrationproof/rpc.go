package registrationproof

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const RPCMethod = "/OpenIMServer.user.user/UserRegister"
const rpcToken = "x-luobo-registration-service-token"

var rpcProofHeaders = []string{serviceauth.TimestampHeader, serviceauth.NonceHeader, serviceauth.SignatureHeader}

type TokenReader interface {
	GetTokensWithoutError(context.Context, string, int) (map[string]int, error)
}
type Nonces interface {
	Consume(context.Context, string, time.Duration) (bool, error)
}
type RedisNonces struct{ Client redis.UniversalClient }

func (r RedisNonces) Consume(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.Client.SetNX(ctx, key, "1", ttl).Result()
}

// WithRPCProof creates a distinct transport proof;the API's HTTP MAC has already
// been consumed. The production interceptor requires RpcCustomHeader values.
func WithRPCProof(ctx context.Context, secret []byte, token string, req *user.UserRegisterReq, now time.Time) (context.Context, error) {
	if req == nil {
		return nil, ErrProtected
	}
	snapshot, e := Snapshot(req.Users)
	if e != nil {
		return nil, e
	}
	if !snapshot.HasMarkers() {
		return ctx, nil
	}
	body, e := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	if e != nil {
		return nil, ErrProtected
	}
	h, e := serviceauth.Sign(secret, serviceauth.Internal, "GRPC", RPCMethod, token, body, now)
	if e != nil {
		return nil, e
	}
	keys := []string{rpcToken}
	ctx = context.WithValue(ctx, rpcToken, []string{token})
	for _, key := range rpcProofHeaders {
		lower := strings.ToLower(key)
		keys = append(keys, lower)
		ctx = context.WithValue(ctx, lower, []string{h.Get(key)})
	}
	return context.WithValue(ctx, constant.RpcCustomHeader, keys), nil
}

type Authorizer struct {
	Tokens TokenReader
	Nonces Nonces
	Secret []byte
	Now    func() time.Time
}

// Authorize never trusts opuser/admin metadata. A current,correct-issuer service
// JWT plus a body-bound MAC and a fresh consumed Redis nonce authorizes only the
// original per-user marker values. Marker-free legacy registration is unchanged.
func (s *Authorizer) Authorize(ctx context.Context, req *user.UserRegisterReq, snapshot *RegistrationSnapshot) (context.Context, error) {
	deny := func() (context.Context, error) {
		return nil, errs.ErrNoPermission.Wrap("开户证明写入未获服务授权")
	}
	if snapshot == nil || req == nil {
		return deny()
	}
	fresh, e := Snapshot(req.Users)
	if e != nil || !same(fresh.original, snapshot.original) {
		return deny()
	}
	if !snapshot.HasMarkers() {
		return ctx, nil
	}
	if s == nil || s.Tokens == nil || s.Nonces == nil {
		return deny()
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return deny()
	}
	tokens := md.Get(rpcToken)
	if len(tokens) != 1 {
		return deny()
	}
	raw := tokens[0]
	claims, e := authverify.ParseClassified(raw)
	if e != nil || claims.Purpose != authverify.ServicePurpose {
		return deny()
	}
	current, e := s.Tokens.GetTokensWithoutError(ctx, claims.UserID, claims.PlatformID)
	if e != nil {
		return deny()
	}
	if state, exists := current[raw]; !exists || state != constant.NormalToken {
		return deny()
	}
	headers := make(http.Header)
	for _, key := range rpcProofHeaders {
		values := md.Get(strings.ToLower(key))
		if len(values) != 1 {
			return deny()
		}
		headers.Set(key, values[0])
	}
	body, e := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	if e != nil {
		return deny()
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	nonce, e := serviceauth.Verify(s.Secret, serviceauth.Internal, "GRPC", RPCMethod, raw, body, headers, now)
	if e != nil {
		return deny()
	}
	consumed, e := s.Nonces.Consume(ctx, nonce, serviceauth.ReplayTTL)
	if e != nil || !consumed {
		return deny()
	}
	// Copy the proof map rather than sharing callback/request-owned maps.
	allowed := grant{}
	for id, fields := range snapshot.allowed {
		m := map[string]string{}
		for key, value := range fields {
			m[key] = value
		}
		allowed[id] = m
	}
	return withInitial(ctx, allowed), nil
}
