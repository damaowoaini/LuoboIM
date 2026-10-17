package namespaceinventory

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Explicit successor operator. Legacy single-file/chunk modes remain readable
// and unchanged. This path can resume/import an existing V1 private journal's
// index in bounded steps; it never restarts that journal under a new identity.
func CaptureResumablePrivate(ctx context.Context, connectionPath, dir string, resume bool, steps int) (out ResumableSummary, resultErr error) {
	budget := ProgressBudget{Steps: steps, SourcePages: 128}
	if ctx == nil || !budget.valid() {
		return out, ErrInvalid
	}
	connection, e := LoadPrivateConnection(connectionPath)
	if e != nil {
		return out, e
	}
	limits := defaultPageLimits()
	if resume {
		s, e := loadSession(dir)
		if e != nil {
			return out, e
		}
		if s.Identity.Binding != connection.Binding {
			return out, ErrIdentity
		}
		limits = s.Limits
	} else if e := CheckFreshPrivatePath(dir); e != nil {
		return out, e
	}
	current, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	config := options.Client().ApplyURI(connection.MongoURI).SetReplicaSet(connection.Binding.ReplicaSet).SetDirect(false).SetReadPreference(readpref.Primary()).SetRetryReads(false).SetRetryWrites(false).SetMaxPoolSize(2).SetMinPoolSize(0).SetConnectTimeout(5 * time.Second).SetServerSelectionTimeout(5 * time.Second).SetSocketTimeout(10 * time.Second)
	client, e := mongo.Connect(current, config)
	if e != nil {
		return out, ErrSource
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if client.Disconnect(cleanup) != nil {
			out = ResumableSummary{}
			resultErr = ErrSource
		}
	}()
	rc, e := redis.ParseURL(connection.RedisURL)
	if e != nil {
		return out, ErrInvalid
	}
	rc.DB = connection.Binding.RedisDB
	rc.MaxRetries = -1
	rc.PoolSize = 2
	rc.MinIdleConns = 0
	rc.DialTimeout = 5 * time.Second
	rc.ReadTimeout = 10 * time.Second
	rc.WriteTimeout = 10 * time.Second
	rdb := redis.NewClient(rc)
	defer func() {
		if rdb.Close() != nil {
			out = ResumableSummary{}
			resultErr = ErrSource
		}
	}()
	source, e := NewLiveSource(client.Database(connection.Binding.Database), rdb, connection.Binding, limits)
	if e != nil {
		return out, e
	}
	if resume {
		return ResumeResumableCapture(current, source, connection.Binding, dir, budget)
	}
	return BeginResumableCapture(current, source, connection.Binding, dir, limits, budget)
}
