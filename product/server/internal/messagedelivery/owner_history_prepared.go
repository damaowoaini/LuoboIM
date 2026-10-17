package messagedelivery

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Only immutable candidate metadata is retained. No message body, roster,
// per-seq permission, pending outcome, current source or global node cache.
// Two entries cover a prospective source plus its canonical boundary alias.
// Oversized/empty candidates use the original uncached path without rejecting
// legitimate history or imposing a total owner/segment/range limit.
const originalPreparedBytes = 64 << 10

type originalPrivateProof struct {
	Path, SHA256 string
	Limit        int
}
type originalHistoryPreparation struct {
	files map[string]originalPrivateProof
}
type originalPreparedHistory struct {
	reader  namespaceimport.OriginalHistoryRecordReader
	binding namespaceimport.OriginalHistoryPreparationBinding
	key     string
	rows    []namespaceimport.OwnerHistorySegment
	files   []originalPrivateProof
}

func originalPreparationPointer(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	return r.Kind() == reflect.Pointer && !r.IsNil()
}
func originalSamePreparationPointer(a, b any) bool {
	return originalPreparationPointer(a) && originalPreparationPointer(b) && reflect.TypeOf(a) == reflect.TypeOf(b) && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
func originalPreparationValid(b namespaceimport.OriginalHistoryPreparationBinding, h namespaceimport.Header, i namespaceimport.Inventory) bool {
	return originalPreparationPointer(b.Capture) && originalPreparationPointer(b.Snapshot) && validSHA(b.SnapshotSHA256) && validSHA(b.MetadataSHA256) && b.HeaderSHA256 == h.SHA256() && b.RawRootSHA256 == i.RawRootSHA256 && b.LeaseID == i.LeaseID && validSHA(b.HeldGuardSHA256) && (i.HeldAdmissionGuardSHA256 == "" || b.HeldGuardSHA256 == i.HeldAdmissionGuardSHA256)
}
func originalPreparationKey(h namespaceimport.Header, i namespaceimport.Inventory, epoch, kind, id string) string {
	return directHistoryDigest(struct{ Header, Inventory, Epoch, Kind, ID string }{h.SHA256(), i.SHA256(), epoch, kind, id})
}
func originalCloneCandidates(rows []namespaceimport.OwnerHistorySegment) []namespaceimport.OwnerHistorySegment {
	out := append([]namespaceimport.OwnerHistorySegment(nil), rows...)
	for n := range out {
		out[n].Evidence = append([]namespaceimport.SourceRef(nil), out[n].Evidence...)
	}
	return out
}
func (c *originalHistoryChain) readPrivateProof(path string, limit int) ([]byte, error) {
	body, e := namespaceinventory.ReadPrivateDocument(path, limit)
	if e != nil {
		return nil, e
	}
	if c.preparation != nil {
		if c.preparation.files == nil {
			c.preparation.files = map[string]originalPrivateProof{}
		}
		dep := originalPrivateProof{path, hash(body), limit}
		if old, ok := c.preparation.files[path]; ok && old != dep {
			return nil, ErrConflict
		}
		c.preparation.files[path] = dep
	}
	return body, nil
}
func (v *OriginalHistoryVerifier) discardOriginalPreparation() {
	v.preparedMu.Lock()
	v.prepared = nil
	v.preparedMu.Unlock()
}
func (v *OriginalHistoryVerifier) recheckOriginalPreparation(ctx context.Context, h namespaceimport.Header, port namespaceimport.OriginalHistoryPreparedReader, entry originalPreparedHistory) error {
	refs := []namespaceimport.SourceRef{}
	for _, row := range entry.rows {
		refs = originalScopeRefs(refs, row.Evidence)
	}
	if e := port.RecheckOriginalHistoryPreparedRefs(ctx, entry.binding, refs); e != nil {
		return e
	}
	for _, dep := range entry.files {
		body, e := namespaceinventory.ReadPrivateDocument(dep.Path, dep.Limit)
		if e != nil {
			return e
		}
		if hash(body) != dep.SHA256 {
			return ErrConflict
		}
	}
	return v.checkConfig(h)
}
func (v *OriginalHistoryVerifier) deriveOriginalHistoryCandidateRanges(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, epoch, kind, id string, reader namespaceimport.OriginalHistoryRecordReader) ([]namespaceimport.OwnerHistorySegment, error) {
	if v == nil || ctx == nil || reader == nil {
		return nil, ErrInvalid
	}
	if e := v.checkConfig(h); e != nil {
		v.discardOriginalPreparation()
		return nil, e
	}
	port, ok := reader.(namespaceimport.OriginalHistoryPreparedReader)
	if !ok || !originalPreparationPointer(reader) {
		return v.buildOriginalHistoryCandidateRanges(ctx, h, i, epoch, kind, id, reader, nil)
	}
	binding, e := port.OriginalHistoryPreparationBinding(ctx)
	if e != nil {
		v.discardOriginalPreparation()
		return nil, e
	}
	if !originalPreparationValid(binding, h, i) {
		v.discardOriginalPreparation()
		return nil, ErrConflict
	}
	cacheKey := originalPreparationKey(h, i, epoch, kind, id)
	v.preparedMu.Lock()
	var hit *originalPreparedHistory
	for _, entry := range v.prepared {
		if entry.key == cacheKey && originalSamePreparationPointer(entry.reader, reader) && entry.binding == binding {
			copy := entry
			hit = &copy
			break
		}
	}
	v.preparedMu.Unlock()
	if hit != nil {
		if e = v.recheckOriginalPreparation(ctx, h, port, *hit); e != nil {
			v.discardOriginalPreparation()
			return nil, e
		}
		return originalCloneCandidates(hit.rows), nil
	}
	preparation := &originalHistoryPreparation{}
	rows, e := v.buildOriginalHistoryCandidateRanges(ctx, h, i, epoch, kind, id, reader, preparation)
	if e != nil {
		return nil, e
	}
	files := []originalPrivateProof{}
	for _, dep := range preparation.files {
		files = append(files, dep)
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	entry := originalPreparedHistory{reader: reader, binding: binding, key: cacheKey, rows: originalCloneCandidates(rows), files: files}
	if e = v.recheckOriginalPreparation(ctx, h, port, entry); e != nil {
		v.discardOriginalPreparation()
		return nil, e
	}
	encoded, e := json.Marshal(struct {
		Rows  []namespaceimport.OwnerHistorySegment
		Files []originalPrivateProof
	}{rows, files})
	if e != nil {
		return nil, e
	}
	if len(rows) == 0 || len(encoded) > originalPreparedBytes {
		return rows, nil
	}
	v.preparedMu.Lock()
	defer v.preparedMu.Unlock()
	// Replace only the bounded optimization. Resume/other reader or final full
	// Inventory has a different key and must rebuild its original proof chain.
	v.prepared = []originalPreparedHistory{entry}
	if kind == "namespace-owner-prospective-v1" {
		for _, row := range rows {
			if row.SourceKind == "namespace-import-boundary-v1" {
				alias := entry
				alias.key = originalPreparationKey(h, i, row.SourceEpoch, row.SourceKind, row.SourceID)
				alias.rows = originalCloneCandidates([]namespaceimport.OwnerHistorySegment{row})
				v.prepared = append(v.prepared, alias)
				break
			}
		}
	}
	return originalCloneCandidates(rows), nil
}
