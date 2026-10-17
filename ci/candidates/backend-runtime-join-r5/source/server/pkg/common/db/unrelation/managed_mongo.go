package unrelation

import (
	"context"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Reuse the exact existing configuration/environment URI selection, with one
// context-owned, finite connection attempt. The legacy NewMongo retry loop is
// unchanged. No indexes, namespaces, Ready or migration rows are written.
func NewManagedMongo(parent context.Context) (*Mongo, error) {
	if observer := ownedmongo.FromContext(parent); observer != nil {
		return newManagedMongo(parent, observer.Monitor())
	}
	return newManagedMongo(parent, nil)
}

// The optional monitor belongs only to an explicit source-owned API. Existing
// constructor semantics/options are unchanged when the monitor is nil.
func NewManagedMongoWithMonitor(parent context.Context, monitor *event.CommandMonitor) (*Mongo, error) {
	if monitor == nil {
		return nil, errors.New("owned Mongo monitor required")
	}
	return newManagedMongo(parent, monitor)
}
func newManagedMongo(parent context.Context, monitor *event.CommandMonitor) (*Mongo, error) {
	if parent == nil || parent.Done() == nil || config.Config.Mongo.Database == "" {
		return nil, errors.New("managed storage context and database required")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	pool := uint64(100)
	if config.Config.Mongo.MaxPoolSize > 0 {
		pool = uint64(config.Config.Mongo.MaxPoolSize)
	}
	settings := options.Client().ApplyURI(buildMongoURI()).SetConnectTimeout(5 * time.Second).SetServerSelectionTimeout(5 * time.Second).SetSocketTimeout(10 * time.Second).SetMaxPoolSize(pool).SetMinPoolSize(0).SetRetryReads(false).SetRetryWrites(false)
	if monitor != nil {
		settings.SetMonitor(monitor)
	}
	client, err := mongo.Connect(ctx, settings)
	if err != nil {
		if client != nil {
			return &Mongo{db: client}, errors.New("managed storage connection unavailable; owned client requires close")
		}
		return nil, errors.New("managed storage connection unavailable")
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		// Return the actual owned client with the error. The assembly installs
		// its one bounded closer even on this partial-start path.
		return &Mongo{db: client}, errors.New("managed storage primary unavailable")
	}
	return &Mongo{db: client}, nil
}
