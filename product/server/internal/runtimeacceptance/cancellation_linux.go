//go:build linux

package runtimeacceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-zookeeper/zk"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/bson"
)

// Cancellation is a negative startup outcome. Local OS exit is deliberately
// never called a canonical drain, business ACK or writer permit. The original
// startup's deferred resource Close/join path must finish before exec.Cmd.Wait
// closes the retained child; no substitute resource or mutable Ready is used.
type CancelledRole struct {
	Role                 string `json:"role"`
	PID                  int    `json:"pid"`
	StartToken           string `json:"startToken"`
	SourceSHA256         string `json:"sourceSHA256"`
	BinarySHA256         string `json:"binarySHA256"`
	OwnerNonceSHA256     string `json:"ownerNonceSHA256"`
	ActualAbsent         bool   `json:"actualAbsent"`
	OriginalWaitObserved bool   `json:"originalWaitObserved"`
	AnnouncementAbsent   bool   `json:"announcementAbsent"`
	CanonicalDrainAbsent bool   `json:"canonicalDrainAbsent"`
	PrivateReleaseAbsent bool   `json:"privateReleaseAbsent"`
}
type CancellationObservation struct {
	PausedPhase                 string          `json:"pausedPhase"`
	ActualPreparedRoles         int             `json:"actualPreparedRoles"`
	Roles                       []CancelledRole `json:"roles"`
	KnownAdmissionAbsent        bool            `json:"knownAdmissionAbsent"`
	ReaderDiscoveryAbsentBefore bool            `json:"readerDiscoveryAbsentBefore"`
	ReaderDiscoveryAbsentAfter  bool            `json:"readerDiscoveryAbsentAfter"`
	OriginalStopReturnedError   bool            `json:"originalStopReturnedError"`
}

func assertReaderDiscoveryAbsent(ctx context.Context, b *Bootstrap) error {
	// These exact paths are generated from the same reviewed configuration that
	// supplies the reader's listening ports. Absence is a real ZK query, not a
	// disconnected gRPC inference or a caller supplied counter.
	c, events, e := zk.Connect([]string{"127.0.0.1:2181"}, 5*time.Second)
	if e != nil {
		return e
	}
	defer c.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return ownedcontrol.ErrOwnedEvidence
			}
			if event.State == zk.StateHasSession {
				goto connected
			}
		}
	}
connected:
	for _, role := range []string{"rpc-conversation", "rpc-msg"} {
		path := b.Services.ReaderDiscoveryPaths[role]
		if path == "" || !strings.HasPrefix(path, "/fullgraph_"+b.Resource.Nonce+"/") || strings.Count(path, "/") != 2 {
			return ownedcontrol.ErrOwnedEvidence
		}
		children, _, e := c.Children(path)
		if errors.Is(e, zk.ErrNoNode) {
			continue
		}
		if e != nil || len(children) != 0 {
			return ownedcontrol.ErrOwnedEvidence
		}
	}
	return nil
}

func collectActualCancellation(ctx context.Context, b *Bootstrap, s *Session) (CancellationObservation, error) {
	out := CancellationObservation{PausedPhase: s.Runtime.StoppedAt, ActualPreparedRoles: len(s.Runtime.Launched)}
	expected := 2
	if b.Plan.Scenario == "reader-writer-fence-cancel" {
		expected = 14
	}
	if out.PausedPhase != b.Inputs.StopAt || out.ActualPreparedRoles != expected || len(s.Runtime.Started) != 0 || s.Runtime.AdmissionSHA256 != "" {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	if e := assertReaderDiscoveryAbsent(ctx, b); e != nil {
		return out, e
	}
	out.ReaderDiscoveryAbsentBefore = true
	count, e := b.AdminMongo.Database(b.Header.Database).Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"_id": ownedcontrol.NamespaceRuntimeAdmissionID(b.Header)})
	if e != nil || count != 0 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	out.KnownAdmissionAbsent = true
	owners := make([]ownedcontrol.ProcessOwner, 0, expected)
	for _, p := range s.Runtime.Launched {
		role := p.Bootstrap.Requirement.Role
		var owner ownedcontrol.ProcessOwner
		body, e := namespaceinventory.ReadPrivateDocument(filepath.Join(b.Plan.Work, "runtime-owners", role+"-runtime-actual-owner.json"), 64<<10)
		if e != nil || namespaceinventory.DecodeStrict(body, &owner) != nil || owner.Role != role || owner.PID <= 1 || owner.StartToken == "" || owner.SourceSHA256 != p.Bootstrap.Requirement.SourceSHA256 || owner.BinarySHA256 != p.Bootstrap.Requirement.BinarySHA256 || owner.BootstrapSHA256 != p.Bootstrap.Requirement.BootstrapSHA256 {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		owners = append(owners, owner)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	// This is the original group stop. A partial startup is expected to be
	// noncanonical; its error is retained and never converted to JoinEvidence.
	stopErr := s.Runtime.Stop(closeCtx)
	out.OriginalStopReturnedError = stopErr != nil
	for at, p := range s.Runtime.Launched {
		owner := owners[at]
		exit, e := p.Service.StopForLocalCleanup(closeCtx)
		s.noteLocalExit(p.Service, exit)
		if exit.Version != "owned-local-child-exited-v1" || exit.Owner != owner || exit.ObservedAbsentAt.IsZero() || e != nil && (!errors.Is(e, ownedcontrol.ErrOwnedEvidence) || exit.WaitSucceeded) {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		if _, e = os.Stat("/proc/" + strconv.Itoa(owner.PID)); !os.IsNotExist(e) {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		work := filepath.Dir(p.Bootstrap.RegistrationPath)
		for _, path := range []string{filepath.Join(work, "runtime-announcement-"+owner.OwnerNonce+".json"), filepath.Join(work, "drain.json"), p.Bootstrap.ReleasePath} {
			if _, e = os.Lstat(path); !os.IsNotExist(e) {
				return out, ownedcontrol.ErrOwnedEvidence
			}
		}
		out.Roles = append(out.Roles, CancelledRole{Role: owner.Role, PID: owner.PID, StartToken: owner.StartToken, SourceSHA256: owner.SourceSHA256, BinarySHA256: owner.BinarySHA256, OwnerNonceSHA256: digestBytes([]byte(owner.OwnerNonce)), ActualAbsent: true, OriginalWaitObserved: true, AnnouncementAbsent: true, CanonicalDrainAbsent: true, PrivateReleaseAbsent: true})
	}
	if e := assertReaderDiscoveryAbsent(ctx, b); e != nil {
		return out, e
	}
	out.ReaderDiscoveryAbsentAfter = true
	count, e = b.AdminMongo.Database(b.Header.Database).Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"_id": ownedcontrol.NamespaceRuntimeAdmissionID(b.Header)})
	if e != nil || count != 0 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	if e := s.cleanupRetainedImport(closeCtx); e != nil {
		return out, e
	}
	if e := b.Resource.ValidateLive(ctx); e != nil {
		return out, e
	}
	return out, nil
}
