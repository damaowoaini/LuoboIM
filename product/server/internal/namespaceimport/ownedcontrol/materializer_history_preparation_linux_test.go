//go:build linux

package ownedcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Actual source/materializer/snapshot and private FS journal/index operations.
// The pre-existing PageSource/exclusion fixture is explicitly synthetic; this
// does not attest Mongo/Redis I/O, live process exclusion or production Ready.
func TestHistoricalPreparedProofBindingRechecksActualSnapshotAndIndex(t *testing.T) {
	s, live, _ := materializerSourceFixture(t)
	ctx := context.Background()
	m := s.materializer
	if _, e := m.OriginalHistoryPreparationBinding(ctx); e == nil {
		t.Fatal("unsealed raw index became prepared")
	}
	for n := 0; n < 2048; n++ {
		m.hold.exclusionAt = time.Now().UTC()
		st, _, e := m.workspace.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if st.Phase != "raw" {
			break
		}
		if _, e = m.StepRaw(ctx); e != nil {
			t.Fatal(e)
		}
		if n == 2047 {
			t.Fatal("raw fixture did not finish")
		}
	}
	binding, e := m.OriginalHistoryPreparationBinding(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if binding.Capture != m || binding.Snapshot != m.snapshot || binding.SnapshotSHA256 != m.snapshot.Fingerprint() {
		t.Fatal("actual object identity lost")
	}
	entries, _, _, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("source-rows", m.snapshot.Fingerprint()), "", 1)
	if e != nil || len(entries) != 1 {
		t.Fatal("actual indexed raw evidence", e)
	}
	var ref namespaceimport.SourceRef
	if namespaceinventory.DecodeStrict(entries[0].Value, &ref) != nil {
		t.Fatal("raw locator")
	}
	refs := []namespaceimport.SourceRef{ref}
	t.Run("same-immutable-source-and-new-progress-reuses-binding", func(t *testing.T) {
		if e := m.RecheckOriginalHistoryPreparedRefs(ctx, binding, refs); e != nil {
			t.Fatal(e)
		}
		st, sum, e := m.workspace.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		change, e := derivedUpdate("prepared-count-only", m.snapshot.Fingerprint(), historicalJSONSHA("one"), namespaceinventory.DerivedEntry{}, struct{ Value int }{1})
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = m.workspace.Commit(ctx, sum, st.Phase, st.Cursor, st.Subcursor, []namespaceinventory.DerivedMutation{change}); e != nil {
			t.Fatal(e)
		}
		again, e := m.OriginalHistoryPreparationBinding(ctx)
		if e != nil || again != binding {
			t.Fatal("ordinary derived progress invalidated immutable raw cut", e)
		}
	})
	t.Run("resumed-materializer-is-a-new-object", func(t *testing.T) {
		next, e := NewHistoricalMaterializer(ctx, m.hold, m.snapshot, live, s.options.DerivedDirectory, s.header.SHA256(), m.policySHA, true, m.describe)
		if e != nil {
			t.Fatal(e)
		}
		if e = next.RecheckOriginalHistoryPreparedRefs(ctx, binding, refs); e == nil {
			t.Fatal("resume borrowed the old materializer identity")
		}
		fresh, e := next.OriginalHistoryPreparationBinding(ctx)
		if e != nil || fresh.Capture == binding.Capture || fresh.Snapshot != binding.Snapshot || fresh.MetadataSHA256 != binding.MetadataSHA256 {
			t.Fatal("resume must rebuild with same original raw metadata", e)
		}
	})
	t.Run("changed-live-identity-refuses-before-reuse", func(t *testing.T) {
		live.id.MongoTopology++
		defer func() { live.id.MongoTopology-- }()
		if e := m.RecheckOriginalHistoryPreparedRefs(ctx, binding, refs); e == nil {
			t.Fatal("changed live identity reused source")
		}
	})
	t.Run("changed-evidence-and-lost-exclusion-refuse", func(t *testing.T) {
		bad := append([]namespaceimport.SourceRef(nil), refs...)
		bad[0].RecordSHA256 = historicalJSONSHA("foreign raw")
		if e := m.RecheckOriginalHistoryPreparedRefs(ctx, binding, bad); e == nil {
			t.Fatal("source-ref hash was not re-read")
		}
		prior := m.hold.exclusionSHA
		m.hold.exclusionSHA = ""
		defer func() { m.hold.exclusionSHA = prior }()
		if e := m.RecheckOriginalHistoryPreparedRefs(ctx, binding, refs); e == nil {
			t.Fatal("lost live hold reused source")
		}
	})
	t.Run("changed-empty-collection-index-cannot-relabel-sealed-absence", func(t *testing.T) {
		// Mutate a DERIVED lookup only. Original RawRoot and present refs remain the
		// same; the full original seal must still reject this absence-map change.
		st, sum, e := m.workspace.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		change, e := derivedUpdate("mongo-objects", "group_request", historicalJSONSHA("foreign"), namespaceinventory.DerivedEntry{}, struct{ ID string }{"foreign"})
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = m.workspace.Commit(ctx, sum, st.Phase, st.Cursor, st.Subcursor, []namespaceinventory.DerivedMutation{change}); e != nil {
			t.Fatal(e)
		}
		if e = m.RecheckOriginalHistoryPreparedRefs(ctx, binding, refs); e == nil {
			t.Fatal("changed original absence index reused candidate proof")
		}
	})
}
