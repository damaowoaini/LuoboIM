package namespaceinventory

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var collections = []string{"msg", "conversation", "group", "group_member", "group_request", "group_membership_history", "message_visibility_floors", "message_delivery_sequences_v1", "message_delivery_receipts_v1", "message_delivery_effects_v1", "group_authority_effects", "luobo_storage_identity"}
var prefixes = []string{"MAX_SEQ:", "MIN_SEQ:", "CON_USER_MIN_SEQ:", "MESSAGE_CACHE:"}

func allowedCollection(name string) bool {
	if name == "@catalog" {
		return true
	}
	for _, v := range collections {
		if name == v {
			return true
		}
	}
	return false
}
func allowedKey(key string) bool {
	if !text(key) {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			return true
		}
	}
	return false
}
func allowedType(kind string) bool {
	return kind == "string" || kind == "list" || kind == "hash" || kind == "set" || kind == "zset"
}

type LiveSource struct {
	db       *mongo.Database
	redis    *redis.Client
	expected Binding
	limits   Limits
}

// Constructor performs no I/O, DDL, storage identity creation or data write.
func NewLiveSource(db *mongo.Database, rdb *redis.Client, b Binding, l Limits) (*LiveSource, error) {
	if db == nil || db.Client() == nil || rdb == nil || !b.valid() || !l.valid() || db.Name() != b.Database || rdb.Options().DB != b.RedisDB {
		return nil, ErrIdentity
	}
	primary := db.Client().Database(db.Name(), options.Database().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority()))
	return &LiveSource{db: primary, redis: rdb, expected: b, limits: l}, nil
}
func infoValue(raw, key string) (string, bool) {
	value := ""
	found := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, key+":") {
			if found {
				return "", false
			}
			found = true
			value = strings.TrimPrefix(line, key+":")
		}
	}
	return value, found
}
func (s *LiveSource) Identity(ctx context.Context) (Identity, error) {
	if s == nil || ctx == nil {
		return Identity{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var hello bson.Raw
	if s.db.RunCommand(current, bson.D{{Key: "hello", Value: 1}}).Decode(&hello) != nil || !uniqueBSON(hello, 0) {
		return Identity{}, ErrSource
	}
	set, ok := hello.Lookup("setName").StringValueOK()
	primary, pok := hello.Lookup("isWritablePrimary").BooleanOK()
	top, tok := hello.Lookup("topologyVersion").DocumentOK()
	if !ok || !pok || !primary || set != s.expected.ReplicaSet || !tok {
		return Identity{}, ErrIdentity
	}
	pid, iok := top.Lookup("processId").ObjectIDOK()
	revision, rok := top.Lookup("counter").Int64OK()
	if !iok || pid.IsZero() || !rok || revision < 0 {
		return Identity{}, ErrIdentity
	}
	var stored bson.Raw
	if s.db.Collection("luobo_storage_identity").FindOne(current, bson.M{"_id": "message-visibility-v1"}).Decode(&stored) != nil || !uniqueBSON(stored, 0) {
		return Identity{}, ErrIdentity
	}
	instance, ok := stored.Lookup("instance").StringValueOK()
	if !ok || instance != s.expected.StorageInstance {
		return Identity{}, ErrIdentity
	}
	tenantValue := stored.Lookup("tenant")
	tenant, hasTenant := tenantValue.StringValueOK()
	if tenantValue.Type != 0 && (!hasTenant || tenant != s.expected.Tenant) {
		return Identity{}, ErrIdentity
	}
	server, err := s.redis.Info(current, "server").Result()
	if err != nil {
		return Identity{}, ErrSource
	}
	run, ok := infoValue(server, "run_id")
	if !ok || run != s.expected.RedisRunID {
		return Identity{}, ErrIdentity
	}
	replication, err := s.redis.Info(current, "replication").Result()
	if err != nil {
		return Identity{}, ErrSource
	}
	role, ok := infoValue(replication, "role")
	replID, rok := infoValue(replication, "master_replid")
	if !ok || role != "master" || !rok || !hexText(replID, 40) {
		return Identity{}, ErrIdentity
	}
	cluster, err := s.redis.Info(current, "cluster").Result()
	if err != nil {
		return Identity{}, ErrSource
	}
	enabled, ok := infoValue(cluster, "cluster_enabled")
	if !ok || enabled != "0" {
		return Identity{}, ErrIdentity
	}
	return Identity{Binding: s.expected, MongoProcess: pid.Hex(), MongoTopology: revision, RedisReplication: replID, TenantAttested: hasTenant}, nil
}

func (s *LiveSource) Mongo(ctx context.Context, visit func(MongoRow) error) error {
	if s == nil || ctx == nil || visit == nil {
		return ErrInvalid
	}
	// Exact allowlist: no user/token/config collection or arbitrary view pipeline.
	cursor, err := s.db.RunCommandCursor(ctx, bson.D{{Key: "listCollections", Value: 1}, {Key: "filter", Value: bson.M{"name": bson.M{"$in": collections}}}, {Key: "cursor", Value: bson.M{"batchSize": 128}}, {Key: "maxTimeMS", Value: 10000}})
	if err != nil {
		return ErrSource
	}
	present := map[string]bool{}
	for cursor.Next(ctx) {
		raw := append(bson.Raw(nil), cursor.Current...)
		name, ok := raw.Lookup("name").StringValueOK()
		kind, kok := raw.Lookup("type").StringValueOK()
		if !ok || name == "@catalog" || !allowedCollection(name) || !kok || kind != "collection" || present[name] || !uniqueBSON(raw, 0) {
			_ = cursor.Close(ctx)
			return ErrInvalid
		}
		present[name] = true
		if err := visit(MongoRow{Collection: "@catalog", KeySHA: digest([]byte(name)), Raw: raw}); err != nil {
			_ = cursor.Close(ctx)
			return err
		}
	}
	readErr, closeErr := cursor.Err(), cursor.Close(ctx)
	if readErr != nil || closeErr != nil {
		return ErrSource
	}
	for _, name := range collections {
		if !present[name] {
			raw, e := bson.Marshal(bson.D{{Key: "name", Value: name}, {Key: "observedAbsent", Value: true}})
			if e != nil {
				return ErrInvalid
			}
			if err := visit(MongoRow{Collection: "@catalog", KeySHA: digest([]byte(name)), Raw: raw}); err != nil {
				return err
			}
			continue
		}
		rows, err := s.db.Collection(name).Find(ctx, bson.D{}, options.Find().SetBatchSize(128).SetMaxTime(10*time.Second))
		if err != nil {
			return ErrSource
		}
		for rows.Next(ctx) {
			raw := append(bson.Raw(nil), rows.Current...)
			id := raw.Lookup("_id")
			if id.Type == 0 {
				_ = rows.Close(ctx)
				return ErrInvalid
			}
			key := append([]byte{byte(id.Type)}, id.Value...)
			if err := visit(MongoRow{Collection: name, KeySHA: digest(key), Raw: raw}); err != nil {
				_ = rows.Close(ctx)
				return err
			}
		}
		readErr, closeErr := rows.Err(), rows.Close(ctx)
		if readErr != nil || closeErr != nil {
			return ErrSource
		}
	}
	return nil
}

// Redis7+ read-only script. One key's type/TTL/bounded content are observed
// atomically without SET/DEL/expiry refresh. Cardinality and exact MEMORY
// SAMPLES0 bounds precede DUMP, so a huge cache is refused before copying it.
const readKeyScript = `
local t=redis.call('TYPE',KEYS[1]).ok
local ttl=redis.call('PTTL',KEYS[1])
if t=='none' or ttl==-2 then return {'gone',t,ttl,'',''} end
local limit=tonumber(ARGV[1]); local elements=tonumber(ARGV[2]); local count=0
if t=='string' then count=redis.call('STRLEN',KEYS[1]); if count>limit then return {'limit',t,ttl,'',''} end
elseif t=='list' then count=redis.call('LLEN',KEYS[1])
elseif t=='hash' then count=redis.call('HLEN',KEYS[1])
elseif t=='set' then count=redis.call('SCARD',KEYS[1])
elseif t=='zset' then count=redis.call('ZCARD',KEYS[1])
else return {'unsupported',t,ttl,'',''} end
if t~='string' and count>elements then return {'limit',t,ttl,'',''} end
local memory=redis.call('MEMORY','USAGE',KEYS[1],'SAMPLES',0)
if not memory or memory>limit then return {'limit',t,ttl,'',''} end
local body=redis.call('DUMP',KEYS[1])
if not body or #body>limit then return {'limit',t,ttl,'',''} end
local head=''; if t=='string' then head=redis.call('GET',KEYS[1]) end
return {'ok',t,ttl,body,head}`

func (s *LiveSource) Redis(ctx context.Context, visit func(RedisRow) error) error {
	if s == nil || ctx == nil || visit == nil {
		return ErrInvalid
	}
	for _, prefix := range prefixes {
		var cursor uint64
		for {
			keys, next, err := s.redis.Scan(ctx, cursor, prefix+"*", 128).Result()
			if err != nil {
				return ErrSource
			}
			for _, key := range keys {
				if !strings.HasPrefix(key, prefix) {
					return ErrChanged
				}
				value, err := s.redis.Do(ctx, "EVAL_RO", readKeyScript, 1, key, s.limits.EntryBytes, s.limits.RedisElements).Result()
				if err != nil {
					return ErrSource
				}
				parts, ok := value.([]interface{})
				if !ok || len(parts) != 5 {
					return ErrInvalid
				}
				status, sok := parts[0].(string)
				kind, kok := parts[1].(string)
				ttl, tok := parts[2].(int64)
				body, bok := parts[3].(string)
				head, hok := parts[4].(string)
				if !sok || !kok || !tok || !bok || !hok {
					return ErrInvalid
				}
				if status == "gone" {
					return ErrChanged
				}
				if status == "limit" {
					return ErrLimit
				}
				if status != "ok" {
					return ErrInvalid
				}
				if !strings.HasPrefix(key, "MAX_SEQ:") {
					head = ""
				}
				if err := visit(RedisRow{Key: key, Type: kind, Dump: []byte(body), RawHead: head, PTTL: ttl}); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return nil
}
