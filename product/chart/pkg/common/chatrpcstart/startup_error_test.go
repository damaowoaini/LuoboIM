package chatrpcstart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/utils"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type startupCycle struct{}

func (e *startupCycle) Error() string { panic("Error must not be read") }
func (e *startupCycle) Unwrap() error { return e }

type startupCauseOnly struct{ cause error }

func (e *startupCauseOnly) Error() string { panic("Error must not be read") }
func (e *startupCauseOnly) Cause() error  { return e.cause }

type startupPanicUnwrap struct{}

func (e *startupPanicUnwrap) Error() string { panic("Error must not be read") }
func (e *startupPanicUnwrap) Unwrap() error { panic("private cause") }

type startupOpaque struct{}

func (e *startupOpaque) Error() string { panic("Error must not be read") }

func TestStartupErrorPreservesCauseAndClosedDiagnostic(t *testing.T) {
	const secret = "PRIVATE_DSN_PASSWORD /private/credentials https://private.invalid/token SQL SELECT secret"
	decode := func(err error) startupDiagnostic {
		t.Helper()
		text := err.Error()
		if !strings.HasPrefix(text, startupErrorPrefix) || strings.Contains(text, secret) || strings.Contains(text, "/private/") || strings.ContainsAny(text, "\r\n") {
			t.Fatal("startup diagnostic leaked or lost fixed format")
		}
		var value startupDiagnostic
		if json.Unmarshal([]byte(strings.TrimPrefix(text, startupErrorPrefix)), &value) != nil || value.Version != "chart-startup-error-v1" || len(value.Causes) < 1 || len(value.Causes) > 8 {
			t.Fatal("startup diagnostic shape rejected")
		}
		return value
	}
	cases := []struct {
		cause  error
		kind   string
		number int
	}{
		{context.Canceled, "context-canceled", 0}, {context.DeadlineExceeded, "context-deadline", 0},
		{&mysqlDriver.MySQLError{Number: 1045, Message: secret}, "mysql", 1045},
		{&os.PathError{Op: "open", Path: secret, Err: syscall.ENOENT}, "errno", int(syscall.ENOENT)},
		{status.Error(codes.PermissionDenied, secret), "grpc", int(codes.PermissionDenied)},
		{errs.NewCodeError(errs.DatabaseError, secret).WithDetail(secret), "sdk", errs.DatabaseError},
		{redis.ErrNoScript, "redis", 0}, {errs.NewCodeError(987654321, secret), "unknown", 0},
		{status.Error(codes.Code(123456789), secret), "unknown", 0}, {errors.New(secret), "unknown", 0},
	}
	stages := []string{"discovery", "register-ip", "rpc-init", "rpc-register", "listen", "serve"}
	for i, tc := range cases {
		original := utils.Wrap1(errs.Wrap(tc.cause, secret))
		wrapped := startupFailure(stages[i%len(stages)], original)
		var typed *StartupError
		if !errors.Is(wrapped, tc.cause) || !errors.As(wrapped, &typed) || typed.Unwrap() != original || typed.Stage() != stages[i%len(stages)] {
			t.Fatal("startup wrapper lost original error chain")
		}
		value := decode(wrapped)
		if value.Stage != stages[i%len(stages)] || value.Truncated || len(value.Causes) != 1 || value.Causes[0].Kind != tc.kind {
			t.Fatal("typed startup cause or stage changed")
		}
		row := value.Causes[0]
		switch tc.kind {
		case "mysql":
			var found *mysqlDriver.MySQLError
			if row.MySQLNumber != tc.number || !errors.As(wrapped, &found) || found != tc.cause {
				t.Fatal("original typed MySQL cause lost")
			}
		case "errno":
			if row.Errno != tc.number {
				t.Fatal("original errno lost")
			}
		case "grpc":
			if row.GRPCCode != tc.number {
				t.Fatal("original gRPC code lost")
			}
		case "sdk":
			var found errs.CodeError
			if row.SDKCode != tc.number || !errors.As(wrapped, &found) || found.Code() != tc.number {
				t.Fatal("original SDK code lost")
			}
		case "redis":
			if row.RedisPrefix != "NOSCRIPT" {
				t.Fatal("real go-redis protocol type lost")
			}
		case "unknown":
			if row.MySQLNumber != 0 || row.Errno != 0 || row.GRPCCode != -1 || row.SDKCode != 0 || row.RedisPrefix != "none" {
				t.Fatal("unknown numeric or text data exported")
			}
		}
	}
	// Shared leaves are a DAG, not a cycle; preserve actual joined typed causes.
	mysqlCause := &mysqlDriver.MySQLError{Number: 1142, Message: secret}
	joined := errors.Join(mysqlCause, mysqlCause, syscall.EACCES, status.Error(codes.Unavailable, secret), errs.ErrNoPermission.WithDetail(secret))
	wrapped := startupFailure("rpc-init", joined)
	value := decode(wrapped)
	if !errors.Is(wrapped, mysqlCause) || value.Truncated || len(value.Causes) != 4 {
		t.Fatal("joined cause or bounded dedup changed")
	}
	// Cause-only legacy wrapper inspection is bounded; the exact stored chain is retained.
	legacy := &startupCauseOnly{cause: errs.NewCodeError(errs.RecordNotFoundError, secret)}
	value = decode(startupFailure("rpc-init", legacy))
	if len(value.Causes) != 1 || value.Causes[0].SDKCode != errs.RecordNotFoundError {
		t.Fatal("legacy SDK cause was not inspected")
	}
	opaque := decode(startupFailure("rpc-init", &startupOpaque{}))
	if opaque.Truncated || opaque.Causes[0].Kind != "unknown" {
		t.Fatal("opaque error text was inspected")
	}
	var deep error = errors.New(secret)
	for i := 0; i < 20; i++ {
		deep = fmt.Errorf("bounded: %w", deep)
	}
	many := make([]error, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, errors.New(secret))
	}
	for _, cause := range []error{&startupCycle{}, &startupPanicUnwrap{}, deep, errors.Join(many...)} {
		typed := startupFailure("rpc-init", cause).(*StartupError)
		value = decode(typed)
		if typed.Unwrap() != cause || !value.Truncated || len(value.Causes) != 1 || value.Causes[0] != unknownStartupCause() {
			t.Fatal("unsafe error traversal did not close to unknown")
		}
	}
	invalid := decode(startupFailure(secret, errors.New(secret)))
	if invalid.Stage != "unknown" || invalid.Causes[0].Kind != "unknown" {
		t.Fatal("invalid internal stage escaped closed enum")
	}
	if startupFailure("serve", nil) != nil {
		t.Fatal("nil original Serve return became failure")
	}
	var zero *StartupError
	if zero.Unwrap() != nil || zero.Stage() != "unknown" || decode(zero).Stage != "unknown" {
		t.Fatal("zero startup wrapper is unsafe")
	}
}
