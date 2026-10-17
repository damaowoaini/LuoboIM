// Package mongotx binds each Mongo transaction to its current request context.
package mongotx

import (
	"context"
	"sync"

	"github.com/OpenIMSDK/tools/tx"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ tx.CtxTx = (*transactions)(nil)

func New(client *mongo.Client) tx.CtxTx { return &transactions{client: client} }

type transactions struct {
	client               *mongo.Client
	mu                   sync.Mutex
	initialized          bool
	supportsTransactions bool
}

func (m *transactions) supported(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.initialized {
		var topology bson.M
		if err := m.client.Database("admin").RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}}).Decode(&topology); err != nil {
			return false, err
		}
		_, replicaSet := topology["setName"]
		// Preserve standalone compatibility and support transactional mongos too.
		m.supportsTransactions = replicaSet || topology["msg"] == "isdbgrid"
		m.initialized = true
	}
	return m.supportsTransactions, nil
}

func (m *transactions) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transactional, err := m.supported(ctx)
	if err != nil {
		return err
	}
	if !transactional {
		return fn(ctx)
	}
	session, err := m.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(context.Background())
	// Never retain ctx in a cached closure: the first HTTP request ends before
	// the next request reaches this shared database instance.
	_, err = session.WithTransaction(ctx, func(current mongo.SessionContext) (any, error) {
		return nil, fn(current)
	})
	return err
}
