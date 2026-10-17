package namespaceinventory

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// PrivateConnection is independent operator input, never runtime configuration.
// The target binding is independently checked against hello/storage identity,
// Redis run_id/role/database. A caller-supplied tenant is NOT attestation when
// the legacy storage identity has no tenant field.
type PrivateConnection struct {
	Version  int     `json:"version"`
	Binding  Binding `json:"binding"`
	MongoURI string  `json:"mongoURI"`
	RedisURL string  `json:"redisURL"`
}

func (c PrivateConnection) valid() bool {
	if c.Version != 1 || !c.Binding.valid() || len(c.MongoURI) > 8192 || len(c.RedisURL) > 8192 {
		return false
	}
	u, e := url.Parse(c.MongoURI)
	if e != nil || (u.Scheme != "mongodb" && u.Scheme != "mongodb+srv") || u.Host == "" || u.Fragment != "" || u.Path != "/"+c.Binding.Database {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	seen := map[string]bool{}
	for name, values := range q {
		fold := strings.ToLower(name)
		if seen[fold] || len(values) != 1 {
			return false
		}
		seen[fold] = true
		if fold == "replicaset" && values[0] != c.Binding.ReplicaSet {
			return false
		}
		if fold == "directconnection" && values[0] != "false" {
			return false
		}
	}
	r, e := redis.ParseURL(c.RedisURL)
	if e != nil || r.DB != c.Binding.RedisDB {
		return false
	}
	return true
}

// CheckFreshPrivatePath has no connection side effects. The final O_EXCL save
// is still required: a path appearing during the scan is never overwritten.
func CheckFreshPrivatePath(path string) error {
	parent, name, e := privateParent(path)
	if e != nil {
		return e
	}
	defer closeDirectory(parent)
	return requireAbsent(parent, name)
}

func LoadPrivateConnection(path string) (PrivateConnection, error) {
	raw, e := ReadPrivateDocument(path, 64<<10)
	if e != nil {
		return PrivateConnection{}, e
	}
	var c PrivateConnection
	if DecodeStrict(raw, &c) != nil || !c.valid() {
		return PrivateConnection{}, ErrInvalid
	}
	return c, nil
}

// CapturePrivate is the actual operator path. Only explicit capture opens any
// connection. It never reads ambient URI/credential/config variables and never
// creates identities, collections, indexes, claims, counters or Ready rows.
// Any failed close is an unsuccessful command even if a complete private
// snapshot was already durably saved; inspect can read that file, not resume it.
func CapturePrivate(ctx context.Context, connectionPath, outputPath string) (summary PublicSummary, resultErr error) {
	if ctx == nil {
		return summary, ErrInvalid
	}
	if e := CheckFreshPrivatePath(outputPath); e != nil {
		return summary, e
	}
	c, e := LoadPrivateConnection(connectionPath)
	if e != nil {
		return summary, e
	}
	current, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	config := options.Client().ApplyURI(c.MongoURI).SetReplicaSet(c.Binding.ReplicaSet).SetDirect(false).SetReadPreference(readpref.Primary()).SetRetryReads(false).SetRetryWrites(false).SetMaxPoolSize(2).SetMinPoolSize(0).SetConnectTimeout(5 * time.Second).SetServerSelectionTimeout(5 * time.Second).SetSocketTimeout(10 * time.Second)
	client, e := mongo.Connect(current, config)
	if e != nil {
		return summary, ErrSource
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if client.Disconnect(cleanup) != nil {
			summary = PublicSummary{}
			resultErr = ErrSource
		}
	}()
	redisConfig, e := redis.ParseURL(c.RedisURL)
	if e != nil {
		return summary, ErrInvalid
	}
	redisConfig.DB = c.Binding.RedisDB
	redisConfig.MaxRetries = -1
	redisConfig.PoolSize = 2
	redisConfig.MinIdleConns = 0
	redisConfig.DialTimeout = 5 * time.Second
	redisConfig.ReadTimeout = 10 * time.Second
	redisConfig.WriteTimeout = 10 * time.Second
	rdb := redis.NewClient(redisConfig)
	defer func() {
		if rdb.Close() != nil {
			summary = PublicSummary{}
			resultErr = ErrSource
		}
	}()
	source, e := NewLiveSource(client.Database(c.Binding.Database), rdb, c.Binding, DefaultLimits())
	if e != nil {
		return summary, e
	}
	snapshot, e := Capture(current, source, c.Binding, DefaultLimits())
	if e != nil {
		return summary, e
	}
	if e := SaveExclusive(outputPath, snapshot); e != nil {
		return summary, e
	}
	return snapshot.Public(), nil
}

// Keep the CLI's one-line failure taxonomy independent of driver errors, URIs,
// account IDs and raw provider text. No error detail is written to stdout.
func SafeFailure(_ error) string {
	return "namespace inventory command failed; private output may be incomplete; no activation performed"
}
