package objectverify

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const RPCMethod = "/luobo.internal.object.EvidenceVerifier/Verify"
const rpcTokenHeader = "x-luobo-object-service-token"

var proofHeaders = []string{serviceauth.TimestampHeader, serviceauth.NonceHeader, serviceauth.SignatureHeader}

type TokenReader interface {
	GetTokensWithoutError(context.Context, string, int) (map[string]int, error)
}
type NonceStore interface {
	Consume(context.Context, string, time.Duration) (bool, error)
}
type RedisNonces struct{ Client redis.UniversalClient }

func (r RedisNonces) Consume(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.Client.SetNX(ctx, key, "1", ttl).Result()
}

// WithRPCProof produces a fresh proof: the HTTP proof has already been consumed.
// RpcCustomHeader is intentional: the production RPC interceptor reconstructs
// outgoing metadata and does not preserve arbitrary outgoing-context values.
func WithRPCProof(ctx context.Context, secret []byte, token string, req *pb.VerifyRequest, now time.Time) (context.Context, error) {
	if err := CheckRequest(req); err != nil {
		return nil, err
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	if err != nil {
		return nil, errs.ErrArgs.Wrap("投诉图片请求无效")
	}
	h, err := serviceauth.Sign(secret, serviceauth.Internal, "GRPC", RPCMethod, token, body, now)
	if err != nil {
		return nil, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用")
	}
	keys := []string{rpcTokenHeader}
	ctx = context.WithValue(ctx, rpcTokenHeader, []string{token})
	for _, key := range proofHeaders {
		lower := strings.ToLower(key)
		keys = append(keys, lower)
		ctx = context.WithValue(ctx, lower, []string{h.Get(key)})
	}
	return context.WithValue(ctx, constant.RpcCustomHeader, keys), nil
}

type RPCServer struct {
	pb.UnimplementedEvidenceVerifierServer
	Verifier *Verifier
	Tokens   TokenReader
	Nonces   NonceStore
	Secret   []byte
	Now      func() time.Time
}

func (s *RPCServer) Verify(ctx context.Context, req *pb.VerifyRequest) (*pb.VerifyResponse, error) {
	if err := CheckRequest(req); err != nil {
		return nil, err
	}
	deny := func() (*pb.VerifyResponse, error) {
		return nil, errs.ErrNoPermission.Wrap("投诉图片服务请求未获授权")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return deny()
	}
	tokens := md.Get(rpcTokenHeader)
	if len(tokens) != 1 {
		return deny()
	}
	raw := tokens[0]
	claims, err := authverify.ParseClassified(raw)
	if err != nil || claims.Purpose != authverify.ServicePurpose {
		return deny()
	}
	if s.Tokens == nil || s.Nonces == nil || s.Verifier == nil {
		return nil, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用")
	}
	current, err := s.Tokens.GetTokensWithoutError(ctx, claims.UserID, claims.PlatformID)
	if err != nil {
		return nil, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用")
	}
	if state, exists := current[raw]; !exists || state != constant.NormalToken {
		return deny()
	}
	h := make(http.Header)
	for _, key := range proofHeaders {
		values := md.Get(strings.ToLower(key))
		if len(values) != 1 {
			return deny()
		}
		h.Set(key, values[0])
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	if err != nil {
		return deny()
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	nonce, err := serviceauth.Verify(s.Secret, serviceauth.Internal, "GRPC", RPCMethod, raw, body, h, now)
	if err != nil {
		return deny()
	}
	consumed, err := s.Nonces.Consume(ctx, nonce, serviceauth.ReplayTTL)
	if err != nil {
		return nil, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用")
	}
	if !consumed {
		return deny()
	}
	return s.Verifier.Verify(ctx, req)
}
