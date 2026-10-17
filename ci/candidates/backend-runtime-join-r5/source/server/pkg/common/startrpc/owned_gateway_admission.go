package startrpc

import (
	"context"
	"google.golang.org/grpc"
)

type ownedRPCAdmissionHooks struct {
	activate func() error
	stop     func(context.Context) error
	failure  func() error
}

// Only the real gateway assembly supplies these hooks. The gate closes before
// RPC handlers drain; it is not a replacement for websocket readers/callbacks
// joining in the separately owned websocket-runtime resource.
func OwnRPCGatewayAdmission(server *grpc.Server, activate func() error, stop func(context.Context) error, failure func() error) error {
	s := ownedScope(server)
	if s == nil || activate == nil || stop == nil || failure == nil {
		return errOwnedRPC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.role != "msggateway" || s.sealed || s.admission != nil {
		return errOwnedRPC
	}
	s.admission = &ownedRPCAdmissionHooks{activate: activate, stop: stop, failure: failure}
	return nil
}
