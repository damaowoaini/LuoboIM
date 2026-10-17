//go:build linux

package runtimeacceptance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ActualReaderEndpoint struct {
	Role                            string `json:"role"`
	PID                             int    `json:"pid"`
	StartToken                      string `json:"startToken"`
	Endpoint                        string `json:"endpoint"`
	ConfigurationSHA256             string `json:"configurationSHA256"`
	ListenerInode                   string `json:"listenerInode"`
	ActualTCPConnected              bool   `json:"actualTCPConnected"`
	OriginalListenerRetained        bool   `json:"originalListenerRetained"`
	ActualUnimplementedAfterRelease bool   `json:"actualUnimplementedAfterRelease"`
}

func actualReaderListener(row ownedcontrol.ObservedHistoryReader, port int) (ActualReaderEndpoint, error) {
	out := ActualReaderEndpoint{Role: row.Role, PID: row.PID, StartToken: row.StartToken, Endpoint: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), ConfigurationSHA256: row.Registration.ConfigurationSHA256}
	if row.PID <= 1 || row.StartToken == "" || port < 1 || port > 65535 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	proc := "/proc/" + strconv.Itoa(row.PID)
	f, e := os.Open(proc + "/net/tcp")
	if e != nil {
		return out, e
	}
	raw, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	closeErr := f.Close()
	if e != nil || closeErr != nil || len(raw) > 4<<20 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	local := fmt.Sprintf("0100007F:%04X", port)
	inodes := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 10 && fields[1] == local && fields[3] == "0A" {
			if _, e := strconv.ParseUint(fields[9], 10, 64); e != nil {
				return out, ownedcontrol.ErrOwnedEvidence
			}
			inodes[fields[9]] = true
		}
	}
	if len(inodes) != 1 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	entries, e := os.ReadDir(proc + "/fd")
	if e != nil || len(entries) > 8192 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	for _, entry := range entries {
		target, e := os.Readlink(proc + "/fd/" + entry.Name())
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return out, e
		}
		for inode := range inodes {
			if target == "socket:["+inode+"]" {
				if out.ListenerInode != "" && out.ListenerInode != inode {
					return out, ownedcontrol.ErrOwnedEvidence
				}
				out.ListenerInode = inode
			}
		}
	}
	if out.ListenerInode == "" {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	return out, nil
}
func probeActualReaderEndpoint(ctx context.Context, graph *ownedcontrol.VerifiedHistoryReaderGraph, row ownedcontrol.ObservedHistoryReader, port int, afterRelease bool, expectedInode string) (ActualReaderEndpoint, error) {
	var out ActualReaderEndpoint
	if graph == nil || graph.Refresh(ctx) != nil {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	out, e := actualReaderListener(row, port)
	if e != nil {
		return out, e
	}
	if expectedInode != "" && out.ListenerInode != expectedInode {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	var connected atomic.Bool
	dial := func(current context.Context, _ string) (net.Conn, error) {
		c, e := (&net.Dialer{}).DialContext(current, "tcp", out.Endpoint)
		if e != nil {
			return nil, e
		}
		if c.RemoteAddr().String() != out.Endpoint {
			_ = c.Close()
			return nil, ownedcontrol.ErrOwnedEvidence
		}
		connected.Store(true)
		return c, nil
	}
	c, e := grpc.NewClient("passthrough:///"+out.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(dial), grpc.WithNoProxy())
	if e != nil {
		return out, e
	}
	budget := 300 * time.Millisecond
	if afterRelease {
		budget = 3 * time.Second
	}
	turn, cancel := context.WithTimeout(ctx, budget)
	e = c.Invoke(turn, "/luobo.fullgraph.unregistered.PreReady/Inspect", &emptypb.Empty{}, &emptypb.Empty{}, grpc.WaitForReady(true))
	cancel()
	closeErr := c.Close()
	want := codes.DeadlineExceeded
	if afterRelease {
		want = codes.Unimplemented
	}
	if status.Code(e) != want || !connected.Load() || closeErr != nil {
		return out, errors.Join(ownedcontrol.ErrOwnedEvidence, closeErr)
	}
	if e = graph.Refresh(ctx); e != nil {
		return out, e
	}
	again, e := actualReaderListener(row, port)
	if e != nil || again.ListenerInode != out.ListenerInode || again.Endpoint != out.Endpoint || again.PID != out.PID || again.StartToken != out.StartToken {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	out.ActualTCPConnected = true
	out.OriginalListenerRetained = true
	out.ActualUnimplementedAfterRelease = afterRelease
	return out, nil
}
