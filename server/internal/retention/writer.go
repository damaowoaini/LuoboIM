package retention

import (
	"context"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

func transaction(ctx context.Context, client *mongo.Client, join bool, fn func(context.Context) error) error {
	if session := mongo.SessionFromContext(ctx); session != nil {
		if !join {
			return ErrUnavailable
		}
		// The driver is pinned at 1.17.10. A session alone is not a transaction:
		// never accept mongotx's standalone fallback or a bare WithSession here.
		active, ok := session.(mongo.XSession)
		if !ok || session.Client() != client {
			return ErrUnavailable
		}
		state := active.ClientSession()
		if state == nil || !state.TransactionRunning() {
			return ErrUnavailable
		}
		if state.CurrentRc == nil || state.CurrentRc.Level != "snapshot" || state.CurrentRp == nil || state.CurrentRp.Mode() != readpref.PrimaryMode || state.CurrentWc == nil || !isMajority(state.CurrentWc) || state.CurrentWc.Journal == nil || !*state.CurrentWc.Journal {
			return ErrUnavailable
		}
		return fn(ctx)
	}
	session, err := client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(context.Background())
	_, err = session.WithTransaction(ctx, func(current mongo.SessionContext) (any, error) { return nil, fn(current) },
		options.Transaction().SetReadConcern(readconcern.Snapshot()).SetReadPreference(readpref.Primary()).
			SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	return err // Standalone transactions fail; there is deliberately no fallback.
}

func isMajority(concern *writeconcern.WriteConcern) bool {
	if concern == nil {
		return false
	}
	value, ok := concern.W.(string)
	return ok && value == "majority"
}

type effectsKey struct{}
type commitEffects struct{ callbacks []func(context.Context) error }

func RequiresStrongTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(effectsKey{}).(*commitEffects)
	return ok
}

// WithPolicyTransaction owns the outer commit. Weak/bare external sessions are
// rejected, never replaced with an independently committed child transaction.
func WithPolicyTransaction(ctx context.Context, client *mongo.Client, fn func(context.Context) error) error {
	if RequiresStrongTransaction(ctx) {
		return transaction(ctx, client, true, fn)
	}
	if mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	effects := &commitEffects{}
	err := transaction(ctx, client, true, func(current context.Context) error {
		effects.callbacks = nil // WithTransaction may retry; retain only committed attempt effects.
		return fn(context.WithValue(current, effectsKey{}, effects))
	})
	if err != nil {
		return err
	}
	return flushEffects(ctx, effects)
}

func AfterCommit(ctx context.Context, fn func(context.Context) error) error {
	if effects, ok := ctx.Value(effectsKey{}).(*commitEffects); ok {
		effects.callbacks = append(effects.callbacks, fn)
		return nil
	}
	return fn(ctx)
}

func flushEffects(ctx context.Context, effects *commitEffects) error {
	failed := false
	for _, fn := range effects.callbacks {
		if fn(ctx) != nil {
			failed = true
		}
	}
	if failed {
		return ErrSyncPending
	}
	return nil
}

func HasPolicyFields(fields map[string]any) bool {
	_, enabled := fields["is_msg_destruct"]
	_, duration := fields["msg_destruct_time"]
	return enabled || duration
}

func CheckExternalFields(fields map[string]any) error {
	for key := range fields {
		if strings.HasPrefix(key, "retention_") || key == "latest_msg_destruct_time" {
			return ErrInvalidPolicy
		}
	}
	return nil
}

func strictSeconds(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int:
		return int64(v), nil
	default:
		return 0, ErrInvalidPolicy
	}
}

func finalPolicy(p Policy, fields map[string]any) (bool, int64, error) {
	enabled, duration := p.Enabled, p.Duration
	if v, ok := fields["is_msg_destruct"]; ok {
		var valid bool
		enabled, valid = v.(bool)
		if !valid {
			return false, 0, ErrInvalidPolicy
		}
	}
	if v, ok := fields["msg_destruct_time"]; ok {
		var err error
		duration, err = strictSeconds(v)
		if err != nil {
			return false, 0, err
		}
	}
	return enabled, duration, ValidateDuration(enabled, duration)
}

// PrepareDocument always creates server metadata itself. A caller-provided
// revision/scan generation can never initialize a worker's authority.
func PrepareDocument(value any) (bson.M, error) {
	raw, err := bson.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields bson.M
	if err = bson.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for key := range fields {
		if strings.HasPrefix(key, "retention_") || key == "latest_msg_destruct_time" {
			delete(fields, key)
		}
	}
	enabled, duration, err := finalPolicy(Policy{}, fields)
	if err != nil {
		return nil, err
	}
	fields["is_msg_destruct"] = enabled
	fields["msg_destruct_time"] = duration
	fields["retention_policy_revision"] = int64(1)
	fields["retention_scan_generation"] = int64(0)
	fields["retention_policy_prepared"] = true
	fields["latest_msg_destruct_time"] = time.Time{}
	return fields, nil
}

// Ordinary disabled conversation creation keeps its existing standalone
// compatibility. Creating any enabled retention preference requires the same
// actual transaction capability as later preference writes and cleanup.
func InsertPrepared(ctx context.Context, coll *mongo.Collection, documents []any) error {
	if len(documents) == 0 {
		return nil
	}
	active := false
	for _, value := range documents {
		doc, ok := value.(bson.M)
		if !ok {
			return ErrInvalidPolicy
		}
		flag, ok := doc["is_msg_destruct"].(bool)
		if !ok {
			return ErrInvalidPolicy
		}
		if flag {
			active = true
		}
	}
	insert := func(current context.Context) error { _, err := coll.InsertMany(current, documents); return err }
	if active {
		return transaction(ctx, coll.Database().Client(), true, insert)
	}
	return insert(ctx)
}

// ApplyFields centralizes every existing patch/full policy writer. A real
// document write increments the server revision, including an explicit repeated
// request, so an old cleanup plan cannot survive an off→on ABA.
func ApplyFields(ctx context.Context, coll *mongo.Collection, filter any, fields map[string]any) (int64, error) {
	if err := CheckExternalFields(fields); err != nil {
		return 0, err
	}
	if !HasPolicyFields(fields) {
		return 0, ErrInvalidPolicy
	}
	var changed int64
	err := transaction(ctx, coll.Database().Client(), true, func(current context.Context) error {
		changed = 0
		cur, err := coll.Find(current, filter, options.Find().SetProjection(bson.M{"owner_user_id": 1, "conversation_id": 1, "user_id": 1, "group_id": 1, "conversation_type": 1, "is_msg_destruct": 1, "msg_destruct_time": 1, "retention_policy_revision": 1}))
		if err != nil {
			return err
		}
		defer cur.Close(current)
		var rows []Policy
		if err = cur.All(current, &rows); err != nil {
			return err
		}
		for _, p := range rows {
			if p.Owner == "" || p.Conversation == "" || p.Revision < 0 || p.Revision >= math.MaxInt64 {
				return ErrInvalidPolicy
			}
			_, _, err = finalPolicy(p, fields)
			if err != nil {
				return err
			}
			set := bson.M{}
			for k, v := range fields {
				set[k] = v
			}
			if v, ok := set["owner_user_id"]; ok && v != p.Owner {
				return ErrInvalidPolicy
			}
			if v, ok := set["conversation_id"]; ok && v != p.Conversation {
				return ErrInvalidPolicy
			}
			if v, ok := set["user_id"]; ok && v != p.Peer {
				return ErrInvalidPolicy
			}
			if v, ok := set["group_id"]; ok && v != p.Group {
				return ErrInvalidPolicy
			}
			if v, ok := set["conversation_type"]; ok {
				n, err := strictSeconds(v)
				if err != nil || n != int64(p.ConversationType) {
					return ErrInvalidPolicy
				}
			}
			set["retention_policy_revision"] = p.Revision + 1
			set["retention_policy_prepared"] = true
			match := revisionFilter("retention_policy_revision", p.Revision)
			match["owner_user_id"] = p.Owner
			match["conversation_id"] = p.Conversation
			result, err := coll.UpdateOne(current, match, bson.M{"$set": set})
			if err != nil {
				return err
			}
			if result.MatchedCount != 1 {
				return ErrPolicyChanged
			}
			changed += result.ModifiedCount
		}
		return nil
	})
	return changed, err
}
