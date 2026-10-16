// Copyright © 2026 Luobo dialog contributors. Apache-2.0.
// Metadata and error envelopes remain compatible with OpenIM tools middleware.
package rpcmiddleware

import (
	"context"
	"fmt"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/errinfo"
	"github.com/OpenIMSDK/tools/checker"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mw/specialerror"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"math"
	"strings"
)

func GrpcClient() grpc.DialOption   { return grpc.WithChainUnaryInterceptor(Client) }
func GrpcServer() grpc.ServerOption { return grpc.ChainUnaryInterceptor(Server) }

func Client(ctx context.Context, method string, req, resp interface{}, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if ctx == nil {
		return errs.ErrInternalServer.Wrap("missing RPC context")
	}
	md := metadata.MD{}
	if headers, _ := ctx.Value(constant.RpcCustomHeader).([]string); len(headers) > 0 {
		for _, key := range headers {
			values, ok := ctx.Value(key).([]string)
			if !ok || len(values) == 0 {
				return errs.ErrArgs.Wrap("missing RPC custom metadata")
			}
			md.Set(key, values...)
		}
		md.Set(constant.RpcCustomHeader, headers...)
	}
	operationID, _ := ctx.Value(constant.OperationID).(string)
	if operationID == "" {
		return errs.ErrArgs.Wrap("ctx missing operationID")
	}
	md.Set(constant.OperationID, operationID)
	for _, key := range []string{constant.OpUserID, constant.OpUserPlatform, constant.ConnID} {
		if value, ok := ctx.Value(key).(string); ok {
			md.Set(key, value)
		}
	}
	err := invoke(metadata.NewOutgoingContext(ctx, md), method, req, resp, cc, opts...)
	if err == nil {
		return nil
	}
	rpcErr, ok := err.(interface{ GRPCStatus() *status.Status })
	if !ok {
		return errs.ErrInternalServer.Wrap(err.Error())
	}
	st := rpcErr.GRPCStatus()
	if st.Code() == codes.OK {
		return errs.ErrInternalServer.Wrap("invalid RPC error status")
	}
	if details := st.Details(); len(details) > 0 {
		if info, ok := details[0].(*errinfo.ErrorInfo); ok {
			return errs.NewCodeError(int(st.Code()), st.Message()).WithDetail(strings.Join(info.Warp, "->") + info.Cause).Wrap()
		}
	}
	return errs.NewCodeError(int(st.Code()), st.Message()).Wrap()
}

func Server(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if recover() != nil {
			log.ZError(ctx, "RPC handler panic", nil, "method", info.FullMethod)
			resp = nil
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "missing metadata")
	}
	if headers := md.Get(constant.RpcCustomHeader); len(headers) > 0 {
		for _, key := range headers {
			values := md.Get(key)
			if len(values) == 0 {
				return nil, status.Error(codes.InvalidArgument, "missing custom metadata")
			}
			ctx = context.WithValue(ctx, key, values)
		}
	}
	operations := md.Get(constant.OperationID)
	if len(operations) != 1 || operations[0] == "" {
		return nil, status.Error(codes.InvalidArgument, "operationID error")
	}
	ctx = context.WithValue(ctx, constant.OperationID, operations[0])
	for _, key := range []string{constant.OpUserID, constant.OpUserPlatform, constant.ConnID} {
		if values := md.Get(key); len(values) == 1 {
			ctx = context.WithValue(ctx, key, values[0])
		}
	}
	if err = checker.Validate(req); err == nil {
		resp, err = next(ctx, req)
	}
	if err == nil {
		return resp, nil
	}
	codeErr := specialerror.ErrCode(errs.Unwrap(err))
	if codeErr == nil {
		codeErr = errs.ErrInternalServer
	}
	code := codeErr.Code()
	if code <= 0 || uint64(code) > math.MaxUint32 {
		code = errs.ServerInternalError
	}
	st, detailErr := status.New(codes.Code(code), codeErr.Msg()).WithDetails(&errinfo.ErrorInfo{Cause: err.Error()})
	if detailErr != nil {
		return nil, status.Error(codes.Internal, fmt.Sprint("RPC error envelope failed"))
	}
	log.ZWarn(ctx, "RPC failed", nil, "method", info.FullMethod, "code", code)
	return nil, st.Err()
}
