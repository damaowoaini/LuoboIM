package intent

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const CompletionVersion = 2
const namespaceDocumentID = "owner-dedup-v2"
const HashContract = "md5-comma-joined-part-etags/v1"

// Scope is derived exclusively from durable tenant state and the authenticated
// actor. Neither a client object name nor a request host can select this scope.
type Scope struct{ ID string }

// ObjectPrefix is a new namespace. Historical global hash keys remain readable
// through their existing metadata, but are never consulted by this namespace.
func (s Scope) ObjectPrefix(hash string, size int64) string {
	return path.Join("openim/data/owner-v2", s.ID, hash, strconv.FormatInt(size, 10)) + "/"
}

func scopeID(namespace, engine, owner string) string {
	data, _ := json.Marshal([]string{"luobo-owner-dedup/v2", namespace, engine, owner})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

type namespaceRecord struct {
	ID        string    `bson:"_id"`
	Version   int       `bson:"version"`
	Namespace string    `bson:"namespace"`
	CreatedAt time.Time `bson:"created_at"`
}

// CompletionProof has no TTL. It records a server-verified upload, independently
// of expiring upload capabilities and client-visible object aliases.
type CompletionProof struct {
	ID           string    `bson:"_id"`
	Version      int       `bson:"version"`
	Scope        string    `bson:"scope"`
	Owner        string    `bson:"owner"`
	Engine       string    `bson:"engine"`
	Hash         string    `bson:"hash"`
	Size         int64     `bson:"size"`
	Key          string    `bson:"key"`
	ETag         string    `bson:"etag"`
	CreatedAt    time.Time `bson:"created_at"`
	HashContract string    `bson:"hash_contract"`
	PartHashes   []string  `bson:"part_hashes"`
}

func newCompletionStore(ctx context.Context, db *mongo.Database) (*mongo.Collection, string, error) {
	opts := options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary())
	ns := db.Collection("object_storage_namespaces", opts)
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, "", err
	}
	initial := namespaceRecord{ID: namespaceDocumentID, Version: CompletionVersion, Namespace: id.String(), CreatedAt: time.Now().UTC()}
	_, err = ns.UpdateOne(ctx, bson.M{"_id": namespaceDocumentID}, bson.M{"$setOnInsert": initial}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return nil, "", err
	}
	stored, err := readAfterFirstWriter(ctx, func(readCtx context.Context) (*namespaceRecord, error) {
		var value namespaceRecord
		err := ns.FindOne(readCtx, bson.M{"_id": namespaceDocumentID}).Decode(&value)
		return &value, err
	}, func(err error) bool { return err == mongo.ErrNoDocuments })
	if err != nil {
		return nil, "", err
	}
	parsed, err := uuid.Parse(stored.Namespace)
	if err != nil || parsed.Version() != 4 || stored.Version != CompletionVersion || parsed.String() != stored.Namespace {
		return nil, "", errs.ErrData.Wrap("storage namespace requires operator repair")
	}
	return db.Collection("object_completion_proofs", opts), stored.Namespace, nil
}

// A concurrent upsert can report duplicate-key before its winner is visible to
// majority reads. Retry only the read of that exact fixed identity, never the
// write or namespace generation. Other errors and malformed data fail closed.
func readAfterFirstWriter[T any](ctx context.Context, read func(context.Context) (*T, error), missing func(error) bool) (*T, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		value, err := read(bounded)
		if err == nil || !missing(err) {
			return value, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return nil, bounded.Err()
		case <-timer.C:
		}
	}
}

func (s *Store) Scope(ctx context.Context, engine string) (Scope, error) {
	owner, err := actor(ctx)
	if err != nil {
		return Scope{}, err
	}
	if s == nil || s.namespace == "" || s.proofs == nil || engine == "" {
		return Scope{}, errs.ErrInternalServer.Wrap("storage ownership state unavailable")
	}
	return Scope{ID: scopeID(s.namespace, engine, owner)}, nil
}

func proofID(scope Scope, hash string, size int64) string {
	data, _ := json.Marshal([]string{scope.ID, hash, strconv.FormatInt(size, 10)})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func ValidWireHash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == md5.Size && strings.ToLower(hash) == hash
}

func (s *Store) completionIdentity(ctx context.Context, engine, hash string, size int64) (Scope, string, string, error) {
	scope, err := s.Scope(ctx, engine)
	if err != nil {
		return Scope{}, "", "", err
	}
	if !ValidWireHash(hash) || size <= 0 {
		return Scope{}, "", "", errs.ErrArgs.Wrap("invalid completed object identity")
	}
	owner, err := actor(ctx)
	return scope, owner, proofID(scope, hash, size), err
}

func validateProof(p *CompletionProof, scope Scope, id, owner, engine, hash string, size int64) error {
	if p == nil || p.ID != id || p.Version != CompletionVersion || p.Scope != scope.ID || p.Owner != owner || p.Engine != engine || p.Hash != hash || p.Size != size || p.ETag == "" || p.HashContract != HashContract || !validPartContract(p.Hash, p.PartHashes) {
		return errs.ErrData.Wrap("invalid object completion proof")
	}
	prefix := scope.ObjectPrefix(hash, size)
	keyID, err := uuid.Parse(strings.TrimPrefix(p.Key, prefix))
	if !strings.HasPrefix(p.Key, prefix) || err != nil || keyID.Version() != 4 || prefix+keyID.String() != p.Key {
		return errs.ErrData.Wrap("invalid completed object location")
	}
	return nil
}

func validPartContract(hash string, parts []string) bool {
	if len(parts) < 1 || len(parts) > 10000 {
		return false
	}
	for _, part := range parts {
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != md5.Size {
			return false
		}
	}
	h := md5.Sum([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(h[:]) == hash
}

func (s *Store) FindCompleted(ctx context.Context, engine, hash string, size int64) (*CompletionProof, error) {
	scope, owner, id, err := s.completionIdentity(ctx, engine, hash, size)
	if err != nil {
		return nil, err
	}
	var proof CompletionProof
	if err = s.proofs.FindOne(ctx, bson.M{"_id": id}).Decode(&proof); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, errs.ErrRecordNotFound.Wrap("completed object not found")
		}
		return nil, err
	}
	if err = validateProof(&proof, scope, id, owner, engine, hash, size); err != nil {
		return nil, err
	}
	return &proof, nil
}

// CommitCompleted is called only after provider completion, immutable-copy
// verification and byte-size/hash-contract checks. Concurrent uploads never
// overwrite a previously committed proof or its randomly generated object key.
func (s *Store) CommitCompleted(ctx context.Context, engine, hash string, size int64, key, etag string, parts []string) (*CompletionProof, error) {
	scope, owner, id, err := s.completionIdentity(ctx, engine, hash, size)
	if err != nil {
		return nil, err
	}
	value := CompletionProof{ID: id, Version: CompletionVersion, Scope: scope.ID, Owner: owner, Engine: engine, Hash: hash, Size: size, Key: key, ETag: etag, CreatedAt: time.Now().UTC(), HashContract: HashContract, PartHashes: append([]string(nil), parts...)}
	if err = validateProof(&value, scope, id, owner, engine, hash, size); err != nil {
		return nil, err
	}
	_, err = s.proofs.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$setOnInsert": value}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		// The provider object is retained on an unknown database ACK. Never delete
		// a potentially committed object merely because the caller saw an error.
		return nil, err
	}
	return readAfterFirstWriter(ctx, func(readCtx context.Context) (*CompletionProof, error) {
		return s.FindCompleted(readCtx, engine, hash, size)
	}, func(err error) bool { return errs.ErrRecordNotFound.Is(err) })
}
