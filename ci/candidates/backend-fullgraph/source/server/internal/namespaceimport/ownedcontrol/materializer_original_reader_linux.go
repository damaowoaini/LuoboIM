//go:build linux

package ownedcontrol

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// ReadOriginalHistoryRecord is an actual sealed source lookup, not a live DB
// query or a caller-supplied JSON witness. The finite collection/index seal is
// mandatory even for absence. No fresh capture/cut, reader grant or default
// row is created, and every invocation checks the actual exclusion and raw root.
func (m *HistoricalMaterializer) ReadOriginalHistoryRecord(ctx context.Context, collection, id string) ([]byte, namespaceimport.SourceRef, error) {
	if m == nil || m.hold == nil || m.workspace == nil || ctx == nil || m.hold.ValidateExclusion() != nil || !historicalIdentity(collection) || !historicalIdentity(id) {
		return nil, namespaceimport.SourceRef{}, ErrOwnedEvidence
	}
	row, found, e := m.workspace.ReadOriginalMongoRecord(ctx, collection, id)
	if e != nil {
		return nil, namespaceimport.SourceRef{}, e
	}
	if !found {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	raw, ok := row.Mongo()
	if !ok || m.hold.ValidateExclusion() != nil {
		return nil, namespaceimport.SourceRef{}, ErrOwnedEvidence
	}
	return append([]byte(nil), raw.Raw...), rawFactRef(row), nil
}

func (m *HistoricalMaterializer) ReadOriginalHistorySourceAtSequence(ctx context.Context, cid string, seq int64) ([]byte, namespaceimport.SourceRef, error) {
	if m == nil || m.hold == nil || m.workspace == nil || ctx == nil || m.hold.ValidateExclusion() != nil {
		return nil, namespaceimport.SourceRef{}, ErrOwnedEvidence
	}
	row, found, e := m.workspace.ReadOriginalHistorySourceAtSequence(ctx, cid, seq)
	if e != nil {
		return nil, namespaceimport.SourceRef{}, e
	}
	if !found {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	raw, ok := row.Mongo()
	if !ok || m.hold.ValidateExclusion() != nil {
		return nil, namespaceimport.SourceRef{}, ErrOwnedEvidence
	}
	return append([]byte(nil), raw.Raw...), rawFactRef(row), nil
}
func (m *HistoricalMaterializer) ReadOriginalHistoryMessageDocument(ctx context.Context, id string) ([]byte, namespaceimport.SourceRef, error) {
	if m == nil || m.hold == nil || m.workspace == nil || ctx == nil || m.hold.ValidateExclusion() != nil {
		return nil, namespaceimport.SourceRef{}, ErrOwnedEvidence
	}
	row, found, e := m.workspace.ReadOriginalHistoryMessageDocument(ctx, id)
	if e != nil {
		return nil, namespaceimport.SourceRef{}, e
	}
	if !found {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	raw, ok := row.Mongo()
	if !ok || m.hold.ValidateExclusion() != nil {
		return nil, namespaceimport.SourceRef{}, ErrOwnedEvidence
	}
	return append([]byte(nil), raw.Raw...), rawFactRef(row), nil
}
