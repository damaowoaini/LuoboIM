package retention

import (
	"context"
	"math"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Pointers preserve absent versus an explicitly stored false/0. The existing
// protobuf52 schema is unchanged; this is a new owner-only HTTP projection.
type Readback struct {
	Owner            string `json:"ownerUserID"`
	Conversation     string `json:"conversationID"`
	ConversationType int32  `json:"conversationType"`
	Peer             string `json:"userID"`
	Group            string `json:"groupID"`
	Private          *bool  `json:"isPrivateChat"`
	BurnDuration     *int64 `json:"burnDuration"`
	Enabled          *bool  `json:"isMsgDestruct"`
	Duration         *int64 `json:"msgDestructTime"`
	Revision         *int64 `json:"policyRevision"`
	// Revision metadata only; this is NOT an activation/migration/cache-sync or
	// physical deletion receipt. Floor coverage is checked separately at work.
	RevisionPrepared bool `json:"policyRevisionPrepared"`
}

func optionalBool(raw bson.Raw, key string) (*bool, error) {
	v, err := raw.LookupErr(key)
	if err != nil {
		return nil, nil
	}
	if v.Type != bsontype.Boolean {
		return nil, ErrInvalidPolicy
	}
	b := v.Boolean()
	return &b, nil
}
func optionalInt(raw bson.Raw, key string) (*int64, error) {
	v, err := raw.LookupErr(key)
	if err != nil {
		return nil, nil
	}
	var n int64
	switch v.Type {
	case bsontype.Int32:
		n = int64(v.Int32())
	case bsontype.Int64:
		n = v.Int64()
	default:
		return nil, ErrInvalidPolicy
	}
	return &n, nil
}

func (s *MongoStore) Readback(ctx context.Context, owner, cid string) (Readback, error) {
	if owner == "" || cid == "" {
		return Readback{}, ErrInvalidPolicy
	}
	var raw bson.Raw
	err := s.collection("conversation").FindOne(ctx, bson.M{"owner_user_id": owner, "conversation_id": cid}, options.FindOne().SetProjection(bson.M{"owner_user_id": 1, "conversation_id": 1, "conversation_type": 1, "user_id": 1, "group_id": 1, "is_private_chat": 1, "burn_duration": 1, "is_msg_destruct": 1, "msg_destruct_time": 1, "retention_policy_revision": 1, "retention_policy_prepared": 1})).Decode(&raw)
	if err != nil {
		return Readback{}, err
	}
	var p Policy
	if err = bson.Unmarshal(raw, &p); err != nil {
		return Readback{}, err
	}
	if p.Owner != owner || p.Conversation != cid {
		return Readback{}, ErrSourceChanged
	}
	r := Readback{Owner: owner, Conversation: cid, ConversationType: p.ConversationType, Peer: p.Peer, Group: p.Group}
	if r.Private, err = optionalBool(raw, "is_private_chat"); err != nil {
		return Readback{}, err
	}
	if r.Enabled, err = optionalBool(raw, "is_msg_destruct"); err != nil {
		return Readback{}, err
	}
	if r.BurnDuration, err = optionalInt(raw, "burn_duration"); err != nil {
		return Readback{}, err
	}
	if r.Duration, err = optionalInt(raw, "msg_destruct_time"); err != nil {
		return Readback{}, err
	}
	if r.Revision, err = optionalInt(raw, "retention_policy_revision"); err != nil {
		return Readback{}, err
	}
	r.RevisionPrepared = p.Prepared && r.Revision != nil && *r.Revision > 0 && *r.Revision < math.MaxInt64 && r.Enabled != nil && r.Duration != nil && ValidateDuration(*r.Enabled, *r.Duration) == nil
	return r, nil
}
