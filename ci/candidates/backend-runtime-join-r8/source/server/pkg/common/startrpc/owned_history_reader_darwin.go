//go:build darwin

package startrpc

import (
	"context"
	"google.golang.org/grpc"
	"os"
	"sync"
)

// The actual held supervisor/procfs identity implementation is Linux-only.
// Preserve the ordinary Darwin entry point; a selected Linux-only preparation
// fails before any listener/discovery/resource. It is never silently ignored.
const HistoryReaderPreparationEnv = "LUOBO_HISTORY_READER_PREPARATION_PATH"
const HistoryReaderPreparationSHAEnv = "LUOBO_HISTORY_READER_PREPARATION_SHA256"

type ownedHistoryStartup struct{}

var ownedHistoryStartups sync.Map

func parseOwnedHistoryStartup(input ownedRPCInput) (*ownedHistoryStartup, error) {
	if input.boot.Mode == "required" && (input.boot.Role == "rpc-msg" || input.boot.Role == "rpc-conversation") {
		return nil, errOwnedRPC
	}
	if os.Getenv(HistoryReaderPreparationEnv) != "" || os.Getenv(HistoryReaderPreparationSHAEnv) != "" {
		return nil, errOwnedRPC
	}
	return nil, nil
}
func OwnedRPCHistoryPreparation(server *grpc.Server) (string, string, error) {
	if s := ownedScope(server); s != nil && s.mode == "required" && (s.role == "rpc-msg" || s.role == "rpc-conversation") {
		return "", "", errOwnedRPC
	}
	_, e := parseOwnedHistoryStartup(ownedRPCInput{})
	return "", "", e
}
func OwnRPCHistoryReader(*grpc.Server, any) error { return errOwnedRPC }
func OwnRPCPreparedWriterFinalCheck(*grpc.Server, func(context.Context) error) error {
	return errOwnedRPC
}
func bindOwnedHistoryAndAwaitReadiness(_ context.Context, _ *grpc.Server, input ownedRPCInput, _ string, _ string) error {
	_, e := parseOwnedHistoryStartup(input)
	return e
}

func awaitOwnedHistoryWriterFinal(context.Context, *grpc.Server) error { return nil }
