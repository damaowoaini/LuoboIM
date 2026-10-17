package startrpc

import (
	"context"
	"google.golang.org/grpc"
)

// This only owns the old legacy process for exclusion. Required runtime never
// opens the raw msg_to_push consumer or the raw PushMsg RPC as a delivery lane.
func OwnRPCPushAdmission(server *grpc.Server, activate func() error, stop func(context.Context) error, failure func() error) error {
	s := ownedScope(server)
	if s == nil || activate == nil || stop == nil || failure == nil {
		return errOwnedRPC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.role != "push" || s.mode != "legacy" || s.sealed || s.admission != nil {
		return errOwnedRPC
	}
	s.admission = &ownedRPCAdmissionHooks{name: "push-admission", activate: activate, stop: stop, failure: failure}
	return nil
}
