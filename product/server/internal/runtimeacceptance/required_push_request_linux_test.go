//go:build linux

package runtimeacceptance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbpush "github.com/OpenIMSDK/protocol/push"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The real fullgraph still owns the actual push process and its single token RPC.
// This control covers the request.Check and middleware envelope before that RPC.
func TestRequiredPushRawRequestPreservesProtocolValidationAndMiddlewareRefusal(t *testing.T) {
	t.Run("complete-real-request-reaches-refusal-and-original-error-roundtrips", func(t *testing.T) {
		request := requiredRawPushRequest("synthetic-manager", "synthetic-token-owner")
		if request.Check() != nil {
			t.Fatal("original raw request did not satisfy actual protocol validation")
		}
		calls := 0
		refused := errors.New("original owned legacy push is unconfirmed")
		ctx := context.WithValue(context.Background(), constant.OperationID, "required-push-positive")
		ctx = context.WithValue(ctx, constant.OpUserID, "synthetic-manager")
		ctx = context.WithValue(ctx, constant.OpUserPlatform, "5")
		err := rpcmiddleware.Client(ctx, "/OpenIMServer.push.PushMsgService/PushMsg", request, nil, nil, func(out context.Context, method string, req, resp interface{}, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, ok := metadata.FromOutgoingContext(out)
			if !ok {
				t.Fatal("original client metadata missing")
			}
			_, err := rpcmiddleware.Server(metadata.NewIncomingContext(context.Background(), md), req, &grpc.UnaryServerInfo{FullMethod: method}, func(in context.Context, original interface{}) (interface{}, error) {
				calls++
				if original != request || in.Value(constant.OpUserID) != "synthetic-manager" || in.Value(constant.OperationID) != "required-push-positive" {
					t.Fatal("actual validated original request or metadata changed before refusal")
				}
				return nil, refused
			})
			return err
		})
		code, ok := errs.Unwrap(err).(interface{ Code() int })
		if calls != 1 || err == nil || !ok || code.Code() != errs.ErrInternalServer.Code() || !strings.Contains(err.Error(), refused.Error()) {
			t.Fatal("actual middleware lost original refusal or dispatched a different handler count")
		}
	})
	t.Run("missing-conversation-id-is-rejected-before-refusal", func(t *testing.T) {
		request := requiredRawPushRequest("synthetic-manager", "synthetic-token-owner")
		request.ConversationID = ""
		if request.Check() == nil {
			t.Fatal("actual protocol accepted the original missing-conversation request")
		}
		calls := 0
		ctx := context.WithValue(context.Background(), constant.OperationID, "required-push-negative")
		ctx = context.WithValue(ctx, constant.OpUserID, "synthetic-manager")
		err := rpcmiddleware.Client(ctx, "/OpenIMServer.push.PushMsgService/PushMsg", request, nil, nil, func(out context.Context, method string, req, resp interface{}, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, ok := metadata.FromOutgoingContext(out)
			if !ok {
				t.Fatal("original client metadata missing")
			}
			_, err := rpcmiddleware.Server(metadata.NewIncomingContext(context.Background(), md), req, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, interface{}) (interface{}, error) {
				calls++
				return &pbpush.PushMsgResp{}, nil
			})
			return err
		})
		if calls != 0 || err == nil || !strings.Contains(err.Error(), "ConversationID is empty") || strings.Contains(err.Error(), "original owned legacy push is unconfirmed") {
			t.Fatal("missing conversation bypassed actual protocol validation or impersonated handler refusal")
		}
	})
}
