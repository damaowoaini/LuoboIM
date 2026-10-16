package namespaceinventory

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SCAN COUNT is not a bound. Admission of a decoded reply/pending cursor is
// independently capped; each page still consumes at most Limits.Rows/Bytes.
const maxScanReplyKeys = 4096

// PageCursor contains private storage coordinates, not an owner/tenant claim.
// The BSON _id token is opaque: underscore CIDs are never parsed as identities.
type PageCursor struct {
	Phase       string   `json:"phase"`
	Collection  int      `json:"collection"`
	CatalogRead bool     `json:"catalogRead"`
	AfterID     []byte   `json:"afterID,omitempty"`
	Prefix      int      `json:"prefix"`
	RedisCursor uint64   `json:"redisCursor"`
	Pending     []string `json:"pendingKeys,omitempty"`
}

func firstPageCursor() PageCursor { return PageCursor{Phase: "mongo"} }
func (c PageCursor) valid() bool {
	if c.Collection < 0 || c.Collection >= len(collections) || c.Prefix < 0 || c.Prefix >= len(prefixes) || len(c.AfterID) > 16<<20 || len(c.Pending) > maxScanReplyKeys {
		return false
	}
	if c.Phase == "done" {
		return c.Collection == 0 && !c.CatalogRead && len(c.AfterID) == 0 && c.Prefix == 0 && c.RedisCursor == 0 && len(c.Pending) == 0
	}
	if c.Phase == "mongo" {
		if c.Prefix != 0 || c.RedisCursor != 0 || len(c.Pending) != 0 || !c.CatalogRead && len(c.AfterID) != 0 {
			return false
		}
		if len(c.AfterID) != 0 {
			raw := bson.Raw(c.AfterID)
			fields, e := raw.Elements()
			if e != nil || len(fields) != 1 || fields[0].Key() != "v" || !uniqueBSON(raw, 0) {
				return false
			}
		}
		return true
	}
	if c.Phase != "redis" || c.Collection != 0 || c.CatalogRead || len(c.AfterID) != 0 {
		return false
	}
	for _, key := range c.Pending {
		if !allowedKey(key) || !strings.HasPrefix(key, prefixes[c.Prefix]) {
			return false
		}
	}
	return true
}

// Page is one bounded unit. Max rows/bytes apply to EACH page, not the history.
// An empty page can advance an exhausted collection or a Redis SCAN cursor.
type Page struct {
	Before PageCursor `json:"before"`
	After  PageCursor `json:"after"`
	Mongo  []MongoRow `json:"mongo"`
	Redis  []RedisRow `json:"redis"`
}

type PageSource interface {
	Identity(context.Context) (Identity, error)
	Page(context.Context, PageCursor, Limits) (Page, error)
}

func nextCollection(c PageCursor) PageCursor {
	if c.Collection+1 == len(collections) {
		return PageCursor{Phase: "redis"}
	}
	return PageCursor{Phase: "mongo", Collection: c.Collection + 1}
}

func (s *LiveSource) Page(ctx context.Context, c PageCursor, l Limits) (Page, error) {
	if s == nil || ctx == nil || !c.valid() || c.Phase == "done" || !l.valid() {
		return Page{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	if c.Phase == "mongo" {
		return s.mongoPage(current, c, l)
	}
	return s.redisPage(current, c, l)
}

func (s *LiveSource) mongoPage(ctx context.Context, c PageCursor, l Limits) (Page, error) {
	p := Page{Before: c, After: c}
	name := collections[c.Collection]
	if !c.CatalogRead {
		// Exact collection catalog (including UUID/options) is captured on both
		// passes. A drop/recreate is not accepted merely because rows look equal.
		cur, e := s.db.RunCommandCursor(ctx, bson.D{{Key: "listCollections", Value: 1}, {Key: "filter", Value: bson.M{"name": name}}, {Key: "cursor", Value: bson.M{"batchSize": 1}}, {Key: "maxTimeMS", Value: 10000}})
		if e != nil {
			return Page{}, ErrSource
		}
		var raw bson.Raw
		for cur.Next(ctx) {
			if raw != nil {
				_ = cur.Close(ctx)
				return Page{}, ErrChanged
			}
			raw = append(bson.Raw(nil), cur.Current...)
		}
		e, closeErr := cur.Err(), cur.Close(ctx)
		if e != nil || closeErr != nil {
			return Page{}, ErrSource
		}
		if raw == nil {
			raw, e = bson.Marshal(bson.D{{Key: "name", Value: name}, {Key: "observedAbsent", Value: true}})
			if e != nil {
				return Page{}, ErrInvalid
			}
			p.After = nextCollection(c)
		} else {
			n, ok := raw.Lookup("name").StringValueOK()
			kind, kok := raw.Lookup("type").StringValueOK()
			if !ok || n != name || !kok || kind != "collection" || !uniqueBSON(raw, 0) {
				return Page{}, ErrInvalid
			}
			p.After.CatalogRead = true
		}
		if len(raw) > l.EntryBytes || len(raw) > l.Bytes {
			return Page{}, ErrLimit
		}
		p.Mongo = []MongoRow{{Collection: "@catalog", KeySHA: digest([]byte(name)), Raw: raw}}
		return p, nil
	}
	filter := bson.D{}
	if len(c.AfterID) != 0 {
		// $expr comparison uses BSON order across _id TYPES; {_id:{$gt:v}}
		// would type-bracket and silently miss other historic _id classes.
		id := bson.Raw(c.AfterID).Lookup("v")
		filter = bson.D{{Key: "$expr", Value: bson.D{{Key: "$gt", Value: bson.A{"$_id", bson.D{{Key: "$literal", Value: id}}}}}}}
	}
	cur, e := s.db.Collection(name).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(l.Rows)).SetBatchSize(1).SetMaxTime(10*time.Second))
	if e != nil {
		return Page{}, ErrSource
	}
	used, full := 0, false
	for cur.Next(ctx) {
		raw := append(bson.Raw(nil), cur.Current...)
		id := raw.Lookup("_id")
		if id.Type == 0 {
			_ = cur.Close(ctx)
			return Page{}, ErrInvalid
		}
		if len(raw) > l.EntryBytes || len(raw) > l.Bytes {
			_ = cur.Close(ctx)
			return Page{}, ErrLimit
		}
		if used > l.Bytes-len(raw) {
			full = true
			break
		}
		key := append([]byte{byte(id.Type)}, id.Value...)
		p.Mongo = append(p.Mongo, MongoRow{Collection: name, KeySHA: digest(key), Raw: raw})
		used += len(raw)
		p.After.AfterID, e = bson.Marshal(bson.D{{Key: "v", Value: id}})
		if e != nil {
			_ = cur.Close(ctx)
			return Page{}, ErrInvalid
		}
	}
	e, closeErr := cur.Err(), cur.Close(ctx)
	if e != nil || closeErr != nil {
		return Page{}, ErrSource
	}
	if !full && len(p.Mongo) < l.Rows {
		p.After = nextCollection(c)
	}
	return p, nil
}

func (s *LiveSource) redisPage(ctx context.Context, c PageCursor, l Limits) (Page, error) {
	p := Page{Before: c, After: c}
	keys, next := append([]string(nil), c.Pending...), c.RedisCursor
	if len(keys) == 0 {
		var e error
		keys, next, e = s.redis.Scan(ctx, c.RedisCursor, prefixes[c.Prefix]+"*", int64(l.Rows)).Result()
		if e != nil {
			return Page{}, ErrSource
		}
		// COUNT is only a hint: a legal257-key reply for Rows256 must not
		// get stuck. Keep the ENTIRE bounded reply and its continuation.
		if len(keys) > maxScanReplyKeys {
			return Page{}, ErrLimit
		}
		if next != 0 && next == c.RedisCursor {
			return Page{}, ErrChanged
		}
	}
	// Validate all pending coordinates before retaining them in a checkpoint,
	// including keys not consumed by this page. No reply tail is truncated.
	for _, key := range keys {
		if !allowedKey(key) || !strings.HasPrefix(key, prefixes[c.Prefix]) {
			return Page{}, ErrChanged
		}
	}
	used, consumed := 0, 0
	for _, key := range keys {
		if consumed >= l.Rows {
			break
		}
		value, e := s.redis.Do(ctx, "EVAL_RO", readKeyScript, 1, key, l.EntryBytes, l.RedisElements).Result()
		if e != nil {
			return Page{}, ErrSource
		}
		parts, ok := value.([]interface{})
		if !ok || len(parts) != 5 {
			return Page{}, ErrInvalid
		}
		status, a := parts[0].(string)
		kind, b := parts[1].(string)
		ttl, d := parts[2].(int64)
		body, f := parts[3].(string)
		head, g := parts[4].(string)
		if !a || !b || !d || !f || !g {
			return Page{}, ErrInvalid
		}
		if status == "gone" {
			return Page{}, ErrChanged
		}
		if status == "limit" {
			return Page{}, ErrLimit
		}
		if status != "ok" || !allowedType(kind) || ttl < -1 {
			return Page{}, ErrInvalid
		}
		if !strings.HasPrefix(key, "MAX_SEQ:") {
			head = ""
		}
		n := len(key) + len(body) + len(head)
		if n > l.EntryBytes || n > l.Bytes {
			return Page{}, ErrLimit
		}
		if used > l.Bytes-n {
			break
		}
		p.Redis = append(p.Redis, RedisRow{Key: key, Type: kind, Dump: []byte(body), RawHead: head, PTTL: ttl})
		used += n
		consumed++
	}
	p.After.RedisCursor, p.After.Pending = next, keys[consumed:]
	if len(p.After.Pending) == 0 && next == 0 {
		if c.Prefix+1 == len(prefixes) {
			p.After = PageCursor{Phase: "done"}
		} else {
			p.After = PageCursor{Phase: "redis", Prefix: c.Prefix + 1}
		}
	}
	return p, nil
}
