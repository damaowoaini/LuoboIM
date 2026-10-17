//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// This is an actual resumable raw-to-facts implementation. It does not attest
// historical authorization or publish a signed import inventory. Roots for
// semantic plans/pages are separate subsequent phases in the same private cut.
type HistoricalMaterializer struct {
	hold      *AdmissionHold
	snapshot  *namespaceinventory.ImportSnapshot
	live      namespaceinventory.PageSource
	workspace *namespaceinventory.ImportWorkspace
	describe  DescribeHistoricalNamespace
	policySHA string
	legacy    *legacyAllocationReview
}

func NewHistoricalMaterializer(ctx context.Context, hold *AdmissionHold, snapshot *namespaceinventory.ImportSnapshot, live namespaceinventory.PageSource, dir, headerSHA, policySHA string, resume bool, describe DescribeHistoricalNamespace) (*HistoricalMaterializer, error) {
	if hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != headerSHA || snapshot == nil || live == nil || describe == nil {
		return nil, ErrOwnedEvidence
	}
	workspace, e := namespaceinventory.OpenImportWorkspace(ctx, snapshot, live, dir, headerSHA, policySHA, resume)
	if e != nil {
		return nil, e
	}
	return &HistoricalMaterializer{hold: hold, snapshot: snapshot, live: live, workspace: workspace, describe: describe, policySHA: policySHA}, nil
}
func materializerBody(value any) (json.RawMessage, error) {
	b, e := json.Marshal(value)
	if e != nil || len(b) > 64<<10 {
		return nil, ErrOwnedEvidence
	}
	return b, nil
}
func (m *HistoricalMaterializer) Workspace() *namespaceinventory.ImportWorkspace {
	if m == nil {
		return nil
	}
	return m.workspace
}

// StepRaw handles ONE original row and at most12 factual atoms (<=64 immutable
// index mutations). The subcursor records an exact atom offset within that row.
// Restart rechecks original custody/identity and resumes there, not at raw0.
func (m *HistoricalMaterializer) StepRaw(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "raw" {
		return st, ErrCaptureProgressPending
	}
	rows, next, done, e := m.snapshot.ReadRows(ctx, m.live, st.Cursor, 1)
	if e != nil {
		return st, e
	}
	if len(rows) == 0 {
		if !done || st.Subcursor != "" {
			return st, ErrOwnedEvidence
		}
		return m.workspace.CompleteOriginalRawIndex(ctx, expected)
	}
	facts, e := DecodeHistoricalRow(rows[0], m.describe)
	if e != nil {
		return st, e
	}
	offset := 0
	if st.Subcursor != "" {
		pieces := strings.Split(st.Subcursor, ":")
		if len(pieces) != 2 || pieces[0] != next {
			return st, ErrOwnedEvidence
		}
		n, e := strconv.Atoi(pieces[1])
		if e != nil || strconv.Itoa(n) != pieces[1] || n < 0 || n > len(facts) {
			return st, ErrOwnedEvidence
		}
		offset = n
	}
	end := min(offset+12, len(facts))
	changes := make([]namespaceinventory.DerivedMutation, 0, 64)
	pending := map[string]bool{}
	appendImmutable := func(setID, key string, value any, skipExisting bool) error {
		combined := setID + key
		if pending[combined] {
			return nil
		}
		pending[combined] = true
		raw, e := materializerBody(value)
		if e != nil {
			return e
		}
		if skipExisting {
			prior, exists, e := m.workspace.ReadEntry(ctx, setID, key)
			if e != nil {
				return e
			}
			if exists {
				var old struct {
					ID string `json:"id"`
				}
				var current struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(prior.Value, &old) != nil || json.Unmarshal(raw, &current) != nil || old.ID != current.ID {
					return ErrOwnedEvidence
				}
				return nil
			}
		}
		changes = append(changes, namespaceinventory.DerivedMutation{SetID: setID, Key: key, Value: raw})
		return nil
	}
	for _, fact := range facts[offset:end] {
		encoded, e := materializerBody(fact)
		if e != nil {
			return st, e
		}
		key := sha(encoded)
		if e = appendImmutable(namespaceinventory.DerivedSetID("facts", m.snapshot.Fingerprint()), key, fact, false); e != nil {
			return st, e
		}
		if fact.Kind == "original-history-segment-source" {
			if fact.SegmentSource == nil {
				return st, ErrOwnedEvidence
			}
			if e = appendImmutable(namespaceinventory.DerivedSetID("original-history-sources", m.snapshot.Fingerprint()), key, fact, false); e != nil {
				return st, e
			}
		}
		if fact.Kind == "unresolved-source-row" && fact.CID == "" {
			if e = appendImmutable(namespaceinventory.DerivedSetID("unattributed-gaps", m.snapshot.Fingerprint()), key, fact, false); e != nil {
				return st, e
			}
		}
		if fact.CID != "" {
			if e = appendImmutable(namespaceinventory.DerivedSetID("namespace-facts", fact.CID), key, fact, false); e != nil {
				return st, e
			}
			if e = appendImmutable(namespaceinventory.DerivedSetID("namespaces", m.snapshot.Fingerprint()), sha([]byte(fact.CID)), struct {
				ID string `json:"id"`
			}{fact.CID}, true); e != nil {
				return st, e
			}
		}
		if fact.GroupID != "" {
			if fact.Kind == "held-current-group" || fact.Kind == "held-current-member" || fact.Kind == "original-membership-calendar-only" {
				if e = appendImmutable(namespaceinventory.DerivedSetID("group-metadata", fact.GroupID), key, fact, false); e != nil {
					return st, e
				}
			}
			if e = appendImmutable(namespaceinventory.DerivedSetID("groups", m.snapshot.Fingerprint()), sha([]byte(fact.GroupID)), struct {
				ID string `json:"id"`
			}{fact.GroupID}, true); e != nil {
				return st, e
			}
		}
	}
	cursor, sub := st.Cursor, next+":"+strconv.Itoa(end)
	if end == len(facts) {
		indexed, e := m.workspace.OriginalRawIndexChanges(ctx, rows[0])
		if e != nil {
			return st, e
		}
		changes = append(changes, indexed...)
		cursor = next
		sub = ""
	}
	if len(changes) > 64 {
		return st, ErrOwnedEvidence
	}
	updated, _, e := m.workspace.Commit(ctx, expected, "raw", cursor, sub, changes)
	if e != nil {
		return st, errors.Join(ErrCaptureProgressPending, e)
	}
	return updated, nil
}
