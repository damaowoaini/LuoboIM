package rpcmiddleware

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"testing"
)

// String must never be called: protobuf String includes credentials and tokens.
type credentialRequest struct{}

func (*credentialRequest) String() string { panic("sensitive request serialized") }
func (*credentialRequest) Check() error   { return nil }
func TestMetadataAndBusinessErrorCompatibility(t *testing.T) {
	ctx := context.WithValue(context.Background(), constant.OperationID, "operation-1")
	ctx = context.WithValue(ctx, constant.OpUserID, "user-1")
	ctx = context.WithValue(ctx, constant.RpcCustomHeader, []string{"user-type"})
	ctx = context.WithValue(ctx, "user-type", []string{"admin"})
	called := false
	err := Client(ctx, "/test.Credentials/Login", &credentialRequest{}, nil, nil, func(out context.Context, method string, req, resp interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(out)
		_, err := Server(metadata.NewIncomingContext(context.Background(), md), req, &grpc.UnaryServerInfo{FullMethod: method}, func(in context.Context, req interface{}) (interface{}, error) {
			called = true
			if in.Value(constant.OperationID) != "operation-1" || in.Value(constant.OpUserID) != "user-1" || in.Value("user-type").([]string)[0] != "admin" {
				t.Fatal("metadata contract changed")
			}
			return nil, errs.ErrNoPermission.Wrap("forbidden resource")
		})
		return err
	})
	if !called || err == nil {
		t.Fatal("business error lost")
	}
	code, ok := errs.Unwrap(err).(interface{ Code() int })
	if !ok || code.Code() != errs.ErrNoPermission.Code() {
		t.Fatal("OpenIM business error code changed")
	}
}
func TestMissingMetadataAndPanicFailClosed(t *testing.T) {
	called := false
	next := func(context.Context, interface{}) (interface{}, error) { called = true; return nil, nil }
	if _, err := Server(context.Background(), &credentialRequest{}, &grpc.UnaryServerInfo{FullMethod: "/test"}, next); status.Code(err) != codes.InvalidArgument || called {
		t.Fatal("missing metadata accepted")
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(constant.OperationID, "operation-2"))
	if _, err := Server(ctx, &credentialRequest{}, &grpc.UnaryServerInfo{FullMethod: "/test"}, func(context.Context, interface{}) (interface{}, error) { panic("private credential") }); status.Code(err) != codes.Internal {
		t.Fatal("panic not contained")
	}
}
