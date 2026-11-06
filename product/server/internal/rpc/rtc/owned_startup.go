package rtc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var errOwnedRTCUserActivation = errors.New("owned RTC service user activation incomplete")

type ownedRTCActivationPhase uint8

const (
	rtcActivationAttempted ownedRTCActivationPhase = iota
	rtcActivationParent
	rtcActivationCallbacks
	rtcActivationAdmitted
	rtcActivationLookup
	rtcActivationCallContext
)

func (p ownedRTCActivationPhase) label() string {
	switch p {
	case rtcActivationAttempted:
		return "attempted"
	case rtcActivationParent:
		return "parent"
	case rtcActivationCallbacks:
		return "callbacks"
	case rtcActivationAdmitted:
		return "admitted"
	case rtcActivationLookup:
		return "lookup"
	case rtcActivationCallContext:
		return "call-context"
	default:
		return "unknown"
	}
}

// A private startup failure retains both original identities for errors.Is/As.
// Its printable surface is closed: no original Message, user ID, numeric code,
// credentials or transport details can be included in a panic/log projection.
type ownedRTCActivationFailure struct {
	phase      ownedRTCActivationPhase
	cause      error
	causeClass string
}

func (e *ownedRTCActivationFailure) Error() string {
	return errOwnedRTCUserActivation.Error() + "\nowned RTC activation phase " + e.phase.label() + "\nowned RTC activation cause " + e.causeClass
}
func (e *ownedRTCActivationFailure) Unwrap() []error {
	if e.cause == nil {
		return []error{errOwnedRTCUserActivation}
	}
	return []error{errOwnedRTCUserActivation, e.cause}
}
func rtcActivationFailure(phase ownedRTCActivationPhase, cause error) error {
	return &ownedRTCActivationFailure{phase: phase, cause: cause, causeClass: ownedRTCActivationCauseClass(cause)}
}
func ownedRTCActivationCauseClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.Canceled) {
		return "context-cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context-deadline"
	}
	if errors.Is(err, ownedcontrol.ErrOwnedEvidence) {
		return "namespace-evidence"
	}
	code, ok := errs.Unwrap(err).(interface{ Code() int })
	if !ok {
		return "other"
	}
	switch code.Code() {
	case errs.ErrUserIDNotFound.Code():
		return "sdk-user-missing"
	case errs.ErrRecordNotFound.Code():
		return "record-missing"
	case errs.ErrNoPermission.Code():
		return "permission"
	case errs.ErrArgs.Code():
		return "argument"
	case errs.ErrInternalServer.Code():
		return "internal"
	case int(codes.Canceled):
		return "grpc-cancelled"
	case int(codes.Unknown):
		return "grpc-unknown"
	case int(codes.InvalidArgument):
		return "grpc-invalid-argument"
	case int(codes.DeadlineExceeded):
		return "grpc-deadline"
	case int(codes.NotFound):
		return "grpc-not-found"
	case int(codes.AlreadyExists):
		return "grpc-already-exists"
	case int(codes.PermissionDenied):
		return "grpc-permission"
	case int(codes.ResourceExhausted):
		return "grpc-resource-exhausted"
	case int(codes.FailedPrecondition):
		return "grpc-failed-precondition"
	case int(codes.Aborted):
		return "grpc-aborted"
	case int(codes.OutOfRange):
		return "grpc-out-of-range"
	case int(codes.Unimplemented):
		return "grpc-unimplemented"
	case int(codes.Internal):
		return "grpc-internal"
	case int(codes.Unavailable):
		return "grpc-unavailable"
	case int(codes.DataLoss):
		return "grpc-data-loss"
	case int(codes.Unauthenticated):
		return "grpc-unauthenticated"
	default:
		return "other"
	}
}

// This decorator changes only the actual existing single-user startup read's
// transport readiness option. GetUserInfo's exact application semantics remain
// in the original SDK wrapper, including its missing-user/error handling.
type ownedRTCReadyUserClient struct{ user.UserClient }

func (c *ownedRTCReadyUserClient) GetDesignateUsers(ctx context.Context, request *user.GetDesignateUsersReq, opts ...grpc.CallOption) (*user.GetDesignateUsersResp, error) {
	return c.UserClient.GetDesignateUsers(ctx, request, append(opts, grpc.WaitForReady(true))...)
}

// Synchronous, one original attempt. Phase labels do not authorize readiness;
// every original parent/admission/lookup/error guard remains closed.
func newOwnedRTCUserActivation(parent context.Context, admitted func() error, lookup func(context.Context) error) func() error {
	var mu sync.Mutex
	attempted := false
	return func() error {
		mu.Lock()
		if attempted {
			mu.Unlock()
			return rtcActivationFailure(rtcActivationAttempted, nil)
		}
		attempted = true
		mu.Unlock()
		if parent == nil || parent.Done() == nil {
			return rtcActivationFailure(rtcActivationParent, nil)
		}
		if err := parent.Err(); err != nil {
			return rtcActivationFailure(rtcActivationParent, err)
		}
		if admitted == nil || lookup == nil {
			return rtcActivationFailure(rtcActivationCallbacks, nil)
		}
		if err := admitted(); err != nil {
			return rtcActivationFailure(rtcActivationAdmitted, err)
		}
		call, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		if err := lookup(call); err != nil {
			return rtcActivationFailure(rtcActivationLookup, err)
		}
		if err := call.Err(); err != nil {
			return rtcActivationFailure(rtcActivationCallContext, err)
		}
		return nil
	}
}
