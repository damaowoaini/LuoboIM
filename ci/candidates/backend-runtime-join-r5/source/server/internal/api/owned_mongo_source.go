package api

import (
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"go.mongodb.org/mongo-driver/event"
)

// Keep the original API seam/test names while delegating all real Mongo events
// to the one shared implementation used by the original RPC/managed owners.
type ownedMongoSource struct{ observer ownedmongo.Observer }

func (s *ownedMongoSource) monitor() *event.CommandMonitor { return s.observer.Monitor() }
func (s *ownedMongoSource) observation() ownedcontrol.SourceObservation {
	return ownedcontrol.ObserveMongoSnapshot(s.observer.Snapshot())
}
