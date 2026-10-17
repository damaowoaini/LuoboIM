//go:build linux

package ownedcontrol

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

func (s *HistoricalCaptureSource) ReadinessMongoCatalog(ctx context.Context, hold *AdmissionHold, h namespaceimport.Header, i namespaceimport.Inventory) ([]NamespaceRuntimeMongoCollection, error) {
	if s == nil || ctx == nil {
		return nil, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if hold == nil || s.agent == nil || hold != s.agent.hold || s.revalidate(ctx, h, i) != nil || s.materializer == nil {
		return nil, ErrOwnedEvidence
	}
	rows, e := s.materializer.workspace.ReadOriginalMongoCatalog(ctx)
	if e != nil {
		return nil, e
	}
	var result []NamespaceRuntimeMongoCollection
	for _, row := range rows {
		raw, ok := row.Mongo()
		if !ok {
			return nil, ErrOwnedEvidence
		}
		name := bson.Raw(raw.Raw).Lookup("name").StringValue()
		uuid, e := namespaceRuntimeCatalogUUID(bson.Raw(raw.Raw), name)
		if e != nil {
			return nil, e
		}
		result = append(result, NamespaceRuntimeMongoCollection{Name: name, UUID: uuid, RawSHA256: sha(raw.Raw), Source: rawFactRef(row)})
	}
	if _, e = NamespaceRuntimeMongoCatalogSHA256(result); e != nil {
		return nil, e
	}
	if e = hold.ValidateExclusion(); e != nil {
		return nil, e
	}
	return result, nil
}
