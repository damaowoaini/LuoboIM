package rtc

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/protocol/user"
	"google.golang.org/grpc"
	"sync"
	"time"
)

var errOwnedRTCUserActivation = errors.New("owned RTC service user activation incomplete")

// This decorator changes only the actual existing single-user startup read's
// transport readiness option. GetUserInfo's exact application semantics remain
// in the original SDK wrapper, including its missing-user/error handling.
type ownedRTCReadyUserClient struct{ user.UserClient }

func (c *ownedRTCReadyUserClient) GetDesignateUsers(ctx context.Context, request *user.GetDesignateUsersReq, opts ...grpc.CallOption) (*user.GetDesignateUsersResp, error) {
	return c.UserClient.GetDesignateUsers(ctx, request, append(opts, grpc.WaitForReady(true))...)
}

// Synchronous source-owner startup phase, not a detached goroutine. The actual
// owner cannot Serve/announce or activate its queued worker until it returns.
// Cancellation/unknown/error retires this one attempt; a later call cannot
// refresh the original identity or silently retry the lookup.
func newOwnedRTCUserActivation(parent context.Context, admitted func() error, lookup func(context.Context) error) func() error {
	var mu sync.Mutex
	attempted := false
	return func() error {
		mu.Lock()
		if attempted {
			mu.Unlock()
			return errOwnedRTCUserActivation
		}
		attempted = true
		mu.Unlock()
		if parent == nil || parent.Done() == nil || parent.Err() != nil || admitted == nil || lookup == nil {
			return errOwnedRTCUserActivation
		}
		if e := admitted(); e != nil {
			return errors.Join(errOwnedRTCUserActivation, e)
		}
		call, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		if e := lookup(call); e != nil {
			return errors.Join(errOwnedRTCUserActivation, e)
		}
		if e := call.Err(); e != nil {
			return errors.Join(errOwnedRTCUserActivation, e)
		}
		return nil
	}
}
