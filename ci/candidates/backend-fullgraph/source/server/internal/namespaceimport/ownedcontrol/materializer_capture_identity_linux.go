//go:build linux

package ownedcontrol

import (
	"context"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type materializedCaptureIdentity struct {
	SnapshotSHA256  string    `json:"snapshot_sha256"`
	HeaderSHA256    string    `json:"header_sha256"`
	CaptureID       string    `json:"capture_id"`
	CapturedAt      time.Time `json:"captured_at"`
	SessionSHA256   string    `json:"session_sha256"`
	RawRootSHA256   string    `json:"raw_root_sha256"`
	MongoRootSHA256 string    `json:"mongo_root_sha256"`
	RedisRootSHA256 string    `json:"redis_root_sha256"`
	MongoRows       uint64    `json:"mongo_rows"`
	RedisRows       uint64    `json:"redis_rows"`
	UniqueRows      uint64    `json:"unique_rows"`
}

// StepRoots first indexes the two ORIGINAL planes in bounded units. This is one
// persisted pass over already sealed source refs, not a live business rescan or
// O(history) replay at each resume. Existing coverage jobs start only after the
// complete plane roots and original capture timestamp have been recorded.
func (m *HistoricalMaterializer) StepRoots(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "roots" {
		return st, ErrCaptureProgressPending
	}
	if st.Subcursor == "" {
		next, _, e := m.workspace.Commit(ctx, expected, "roots", "", "source-roots:rows", nil)
		return next, e
	}
	if !strings.HasPrefix(st.Subcursor, "source-roots:") {
		return m.stepHistoricalRoots(ctx)
	}
	if e = m.workspace.CheckOriginalHistoryIndex(ctx); e != nil {
		return st, e
	}
	switch st.Subcursor {
	case "source-roots:rows":
		entries, next, done, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("source-rows", m.snapshot.Fingerprint()), st.Cursor, 1)
		if e != nil || m.snapshot.Identity().UniqueRows < 0 || total != uint64(m.snapshot.Identity().UniqueRows) {
			return st, ErrOwnedEvidence
		}
		if len(entries) == 0 {
			if !done {
				return st, ErrOwnedEvidence
			}
			return m.advanceRootCursor(ctx, "", "source-roots:mongo", nil)
		}
		var ref namespaceimport.SourceRef
		if namespaceinventory.DecodeStrict(entries[0].Value, &ref) != nil || sha([]byte(ref.Kind)) != entries[0].Key {
			return st, ErrOwnedEvidence
		}
		raw, e := m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position)
		if e != nil {
			return st, e
		}
		plane := ""
		if _, ok := raw.Mongo(); ok {
			plane = "mongo-source-rows"
		}
		if _, ok := raw.Redis(); ok {
			if plane != "" {
				return st, ErrOwnedEvidence
			}
			plane = "redis-source-rows"
		}
		if plane == "" {
			return st, ErrOwnedEvidence
		}
		change, e := derivedUpdate(plane, m.snapshot.Fingerprint(), entries[0].Key, namespaceinventory.DerivedEntry{}, ref)
		if e != nil {
			return st, e
		}
		return m.advanceRootCursor(ctx, next, "source-roots:rows", []namespaceinventory.DerivedMutation{change})
	case "source-roots:mongo", "source-roots:redis":
		kind, next := "mongo-source-rows", "source-roots:redis"
		if st.Subcursor == "source-roots:redis" {
			kind = "redis-source-rows"
			next = "source-roots:seal"
		}
		_, complete, e := m.advanceTree(ctx, kind, m.snapshot.Fingerprint())
		if e != nil {
			return st, e
		}
		if !complete {
			current, _, e := m.workspace.Checkpoint(ctx)
			return current, e
		}
		return m.advanceRootCursor(ctx, "", next, nil)
	case "source-roots:seal":
		mongo, e := m.rootState(ctx, "mongo-source-rows", m.snapshot.Fingerprint())
		if e != nil {
			return st, e
		}
		redis, e := m.rootState(ctx, "redis-source-rows", m.snapshot.Fingerprint())
		if e != nil {
			return st, e
		}
		info := m.snapshot.Identity()
		if info.UniqueRows < 0 || mongo.Count > ^uint64(0)-redis.Count || mongo.Count+redis.Count != uint64(info.UniqueRows) {
			return st, ErrOwnedEvidence
		}
		stamp, e := m.snapshot.CaptureStamp(ctx, m.live)
		if e != nil {
			return st, e
		}
		value := materializedCaptureIdentity{SnapshotSHA256: m.snapshot.Fingerprint(), HeaderSHA256: m.hold.HeaderSHA256(), CaptureID: stamp.CaptureID, CapturedAt: stamp.CapturedAt, SessionSHA256: stamp.SessionSHA256, RawRootSHA256: info.RawRootSHA256, MongoRootSHA256: mongo.RootSHA256, RedisRootSHA256: redis.RootSHA256, MongoRows: mongo.Count, RedisRows: redis.Count, UniqueRows: uint64(info.UniqueRows)}
		change, e := derivedUpdate("capture-identity", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), namespaceinventory.DerivedEntry{}, value)
		if e != nil {
			return st, e
		}
		return m.advanceRootCursor(ctx, "", "history-sources:begin", []namespaceinventory.DerivedMutation{change})
	}
	return st, ErrOwnedEvidence
}
func (m *HistoricalMaterializer) captureIdentity(ctx context.Context) (materializedCaptureIdentity, error) {
	if m == nil || m.hold == nil || m.hold.ValidateExclusion() != nil {
		return materializedCaptureIdentity{}, ErrOwnedEvidence
	}
	var value materializedCaptureIdentity
	_, found, e := m.readDerived(ctx, "capture-identity", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), &value)
	if e != nil || !found {
		return value, ErrCaptureProgressPending
	}
	stamp, e := m.snapshot.CaptureStamp(ctx, m.live)
	if e != nil || value.SnapshotSHA256 != m.snapshot.Fingerprint() || value.HeaderSHA256 != m.hold.HeaderSHA256() || value.CaptureID != stamp.CaptureID || !value.CapturedAt.Equal(stamp.CapturedAt) || value.SessionSHA256 != stamp.SessionSHA256 || value.RawRootSHA256 != m.snapshot.Identity().RawRootSHA256 {
		return value, ErrOwnedEvidence
	}
	mongo, e := m.rootState(ctx, "mongo-source-rows", m.snapshot.Fingerprint())
	if e != nil || mongo.RootSHA256 != value.MongoRootSHA256 || mongo.Count != value.MongoRows {
		return value, ErrOwnedEvidence
	}
	redis, e := m.rootState(ctx, "redis-source-rows", m.snapshot.Fingerprint())
	if e != nil || redis.RootSHA256 != value.RedisRootSHA256 || redis.Count != value.RedisRows {
		return value, ErrOwnedEvidence
	}
	return value, nil
}
