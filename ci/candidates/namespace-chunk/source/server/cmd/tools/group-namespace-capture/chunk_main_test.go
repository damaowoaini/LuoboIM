package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/bson"
)

type emptyPagedSource struct{ emptyReadSource }

func (s emptyPagedSource) Page(_ context.Context, c namespaceinventory.PageCursor, _ namespaceinventory.Limits) (namespaceinventory.Page, error) {
	names := []string{"msg", "conversation", "group", "group_member", "group_request", "group_membership_history", "message_visibility_floors", "message_delivery_sequences_v1", "message_delivery_receipts_v1", "message_delivery_effects_v1", "group_authority_effects", "luobo_storage_identity"}
	p := namespaceinventory.Page{Before: c}
	if c.Phase == "mongo" {
		n := names[c.Collection]
		raw, _ := bson.Marshal(bson.D{{Key: "name", Value: n}, {Key: "observedAbsent", Value: true}})
		h := sha256.Sum256([]byte(n))
		p.Mongo = []namespaceinventory.MongoRow{{Collection: "@catalog", KeySHA: hex.EncodeToString(h[:]), Raw: raw}}
		if c.Collection+1 == len(names) {
			p.After = namespaceinventory.PageCursor{Phase: "redis"}
		} else {
			p.After = namespaceinventory.PageCursor{Phase: "mongo", Collection: c.Collection + 1}
		}
	} else if c.Prefix == 3 {
		p.After = namespaceinventory.PageCursor{Phase: "done"}
	} else {
		p.After = namespaceinventory.PageCursor{Phase: "redis", Prefix: c.Prefix + 1}
	}
	return p, nil
}

func TestNamespaceCaptureCLIChunksRequireExplicitModes(t *testing.T) {
	b := namespaceinventory.Binding{Tenant: "private-tenant", Database: "private-db", StorageInstance: "1cfad324-14cd-40bf-b688-2736144cbf32", ReplicaSet: "synthetic-rs", RedisRunID: strings.Repeat("a", 40), RedisDB: 15}
	parent, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(parent, 0700) != nil {
		t.Fatal("private parent unavailable")
	}
	dir := filepath.Join(parent, "chunks")
	limits := namespaceinventory.DefaultLimits()
	limits.Rows = 256
	summary, e := namespaceinventory.BeginChunkCapture(context.Background(), emptyPagedSource{emptyReadSource{binding: b}}, b, dir, limits, 128)
	if e != nil || !summary.ObservedComplete {
		t.Fatal("private chunk fixture failed")
	}
	t.Run("offline-explicit-inspect", func(t *testing.T) {
		var output bytes.Buffer
		if e := run(context.Background(), []string{"-mode", "inspect-chunks", "-snapshot", dir}, &output); e != nil {
			t.Fatal("actual CLI chunk inspect failed")
		}
		var actual namespaceinventory.ChunkSummary
		if json.Unmarshal(output.Bytes(), &actual) != nil || actual.CheckpointSHA != summary.CheckpointSHA || actual.State != namespaceinventory.State || strings.Contains(output.String(), "private-tenant") {
			t.Fatal("chunk inspect output leaked or changed evidence")
		}
	})
	t.Run("default-inspect-does-not-open-resume", func(t *testing.T) {
		var output bytes.Buffer
		if e := run(context.Background(), []string{"-snapshot", dir}, &output); e == nil || output.Len() != 0 {
			t.Fatal("default snapshot inspect silently resumed directory")
		}
	})
	t.Run("no-implicit-connection-or-activation", func(t *testing.T) {
		for _, args := range [][]string{{"-mode", "resume-chunks", "-snapshot", dir}, {"-mode", "inspect-chunks", "-snapshot", dir, "-connection", "/never-open"}, {"-mode", "inspect-chunks", "-snapshot", dir, "-pages", "1"}, {"-mode", "apply", "-snapshot", dir}} {
			var output bytes.Buffer
			if e := run(context.Background(), args, &output); e == nil || output.Len() != 0 {
				t.Fatal("implicit connection/page-write/activation accepted")
			}
		}
	})
}

func TestNamespaceCaptureCLIResumableProgressIsExplicit(t *testing.T) {
	b := namespaceinventory.Binding{Tenant: "private-tenant", Database: "private-db", StorageInstance: "1cfad324-14cd-40bf-b688-2736144cbf32", ReplicaSet: "synthetic-rs", RedisRunID: strings.Repeat("a", 40), RedisDB: 15}
	parent, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(parent, 0700) != nil {
		t.Fatal("private parent unavailable")
	}
	dir := filepath.Join(parent, "progress")
	limits := namespaceinventory.DefaultLimits()
	limits.Rows = 256
	summary, e := namespaceinventory.BeginResumableCapture(context.Background(), emptyPagedSource{emptyReadSource{binding: b}}, b, dir, limits, namespaceinventory.ProgressBudget{Steps: 256, SourcePages: 128})
	if e != nil || !summary.RecordedTwoPassComplete {
		t.Fatal("resumable CLI fixture failed")
	}
	t.Run("recorded-progress-not-fresh-full-custody", func(t *testing.T) {
		var output bytes.Buffer
		if e := run(context.Background(), []string{"-mode", "inspect-resumable", "-snapshot", dir}, &output); e != nil {
			t.Fatal("actual resumable CLI inspect failed")
		}
		var actual namespaceinventory.ResumableSummary
		if json.Unmarshal(output.Bytes(), &actual) != nil || !actual.RecordedTwoPassComplete || actual.CurrentFullCustodyReverified || actual.CheckpointSHA != summary.CheckpointSHA || actual.State != namespaceinventory.State {
			t.Fatal("inspect changed recorded facts into fresh authority")
		}
	})
	t.Run("explicit-input-no-implicit-resume-or-activation", func(t *testing.T) {
		for _, args := range [][]string{{"-mode", "resume-resumable", "-snapshot", dir}, {"-mode", "inspect-resumable", "-snapshot", dir, "-steps", "1"}, {"-mode", "inspect", "-snapshot", dir, "-steps", "1"}, {"-mode", "apply", "-snapshot", dir}} {
			var output bytes.Buffer
			if e := run(context.Background(), args, &output); e == nil || output.Len() != 0 {
				t.Fatal("implicit private connection/write/activation accepted")
			}
		}
	})
}
