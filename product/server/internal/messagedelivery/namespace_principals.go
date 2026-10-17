package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// NamespaceOriginDescriptor describes a protocol identity, not a grant to read
// a conversation. Group origin IDs contain the group ID, never its members.
// Direct origin IDs are retained so erased message bodies are not required to
// resolve an ambiguous n_ conversation name later.
type NamespaceOriginDescriptor struct {
	CID, Kind, GroupID, SHA256 string
	OriginIDs                  []string
}

func DescribeNamespace(message *sdkws.MsgData) (NamespaceOriginDescriptor, error) {
	origin, err := messageNamespace(message)
	if err != nil {
		return NamespaceOriginDescriptor{}, err
	}
	d := NamespaceOriginDescriptor{CID: origin.CID, Kind: origin.Kind, GroupID: origin.GroupID, SHA256: origin.SHA256}
	switch origin.Kind {
	case "group":
		d.OriginIDs = []string{message.GroupID}
	case "single":
		d.OriginIDs = []string{message.SendID, message.RecvID}
		sort.Strings(d.OriginIDs)
	case "server-notification":
		d.OriginIDs = []string{message.SendID, message.RecvID}
	default:
		return NamespaceOriginDescriptor{}, ErrInvalid
	}
	if err := validateNamespaceDescriptor(d); err != nil {
		return NamespaceOriginDescriptor{}, err
	}
	return d, nil
}

func validateNamespaceDescriptor(d NamespaceOriginDescriptor) error {
	if !identity(d.CID) || !validSHA(d.SHA256) {
		return ErrInvalid
	}
	for _, id := range d.OriginIDs {
		if !identity(id) {
			return ErrInvalid
		}
	}
	var message sdkws.MsgData
	switch d.Kind {
	case "group":
		if len(d.OriginIDs) != 1 || d.GroupID != d.OriginIDs[0] {
			return ErrInvalid
		}
		message = sdkws.MsgData{SessionType: constant.SuperGroupChatType, GroupID: d.GroupID}
		legacy := msgprocessor.GetConversationIDBySessionType(constant.GroupChatType, d.GroupID)
		if d.CID != legacy && d.CID != msgprocessor.GetChatConversationIDByMsg(&message) && d.CID != msgprocessor.GetNotificationConversationIDByMsg(&message) {
			return ErrInvalid
		}
	case "single", "server-notification":
		if len(d.OriginIDs) != 2 || d.GroupID != "" || d.Kind == "single" && d.OriginIDs[0] > d.OriginIDs[1] {
			return ErrInvalid
		}
		message = sdkws.MsgData{SendID: d.OriginIDs[0], RecvID: d.OriginIDs[1], SessionType: constant.SingleChatType}
		if d.Kind == "server-notification" {
			message.SessionType = constant.NotificationChatType
		}
		if d.CID != msgprocessor.GetChatConversationIDByMsg(&message) && d.CID != msgprocessor.GetNotificationConversationIDByMsg(&message) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	body, err := json.Marshal([]any{d.Kind, d.OriginIDs})
	if err != nil || hash(body) != d.SHA256 {
		return ErrConflict
	}
	return nil
}

type namespacePrincipals struct {
	ID                 string    `bson:"_id"`
	Kind               string    `bson:"kind"`
	ConversationID     string    `bson:"conversation_id"`
	OriginKind         string    `bson:"origin_kind"`
	OriginSHA256       string    `bson:"origin_sha256"`
	OriginIDs          []string  `bson:"origin_ids"`
	ClaimSHA256        string    `bson:"claim_sha256"`
	Epoch              string    `bson:"created_epoch"`
	ActivationSHA256   string    `bson:"created_activation_sha256"`
	SeedEvidenceSHA256 string    `bson:"seed_evidence_sha256"`
	CreatedAt          time.Time `bson:"created_at"`
}

func namespacePrincipalsID(cid string) string {
	return "permanent-origin-principals:" + hash([]byte(cid))
}

// The existing strict claim decoder runs first. Canonicalizing its typed value
// binds the companion to every retained claim field without relying on BSON
// field order or interpreting a namespace string as an owner identity.
func (s *Store) namespaceClaimIdentity(ctx context.Context, cid string) (messageOrigin, string, string, string, time.Time, error) {
	origin, _, err := s.namespaceOwner(ctx, cid)
	if err != nil {
		return messageOrigin{}, "", "", "", time.Time{}, err
	}
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceClaimID(cid)}).Decode(&raw); err != nil {
		return messageOrigin{}, "", "", "", time.Time{}, err
	}
	var body []byte
	var epoch, activation string
	var created time.Time
	if kind, _ := raw.Lookup("kind").StringValueOK(); kind == historicalNamespaceKind {
		row, err := decodeHistoricalNamespaceClaim(raw, cid)
		if err != nil {
			return messageOrigin{}, "", "", "", time.Time{}, err
		}
		body, err := json.Marshal(row)
		if err != nil {
			return messageOrigin{}, "", "", "", time.Time{}, err
		}
		return origin, hash(body), row.Epoch, row.ActivationSHA256, row.CreatedAt, nil
	}
	switch origin.Kind {
	case "group":
		var row groupNamespaceClaim
		if bson.Unmarshal(raw, &row) != nil {
			return messageOrigin{}, "", "", "", time.Time{}, ErrConflict
		}
		body, err = json.Marshal(row)
		epoch, activation, created = row.Epoch, row.ActivationSHA256, row.CreatedAt
	case "single", "server-notification":
		var row directNamespaceClaim
		if bson.Unmarshal(raw, &row) != nil {
			return messageOrigin{}, "", "", "", time.Time{}, ErrConflict
		}
		body, err = json.Marshal(row)
		epoch, activation, created = row.Epoch, row.ActivationSHA256, row.CreatedAt
	default:
		return messageOrigin{}, "", "", "", time.Time{}, ErrUnprepared
	}
	if err != nil {
		return messageOrigin{}, "", "", "", time.Time{}, err
	}
	return origin, hash(body), epoch, activation, created, nil
}

func decodeNamespacePrincipals(raw bson.Raw) (namespacePrincipals, error) {
	// Validate the one typed array alongside the exact nine string fields and
	// date. Unknown or duplicate fields are rejected before typed decoding.
	elements, err := raw.Elements()
	if err != nil || len(elements) != 11 {
		return namespacePrincipals{}, ErrConflict
	}
	seen := make(map[string]bool, len(elements))
	strings := map[string]bool{"_id": true, "kind": true, "conversation_id": true, "origin_kind": true, "origin_sha256": true, "claim_sha256": true, "created_epoch": true, "created_activation_sha256": true, "seed_evidence_sha256": true}
	for _, e := range elements {
		if seen[e.Key()] {
			return namespacePrincipals{}, ErrConflict
		}
		seen[e.Key()] = true
		if strings[e.Key()] {
			if _, ok := e.Value().StringValueOK(); !ok {
				return namespacePrincipals{}, ErrConflict
			}
		} else if e.Key() == "created_at" {
			if _, ok := e.Value().DateTimeOK(); !ok {
				return namespacePrincipals{}, ErrConflict
			}
		} else if e.Key() == "origin_ids" {
			if _, ok := e.Value().ArrayOK(); !ok {
				return namespacePrincipals{}, ErrConflict
			}
		} else {
			return namespacePrincipals{}, ErrConflict
		}
	}
	var row namespacePrincipals
	if bson.Unmarshal(raw, &row) != nil || row.Kind != "permanent-origin-principals-v1" || row.ID != namespacePrincipalsID(row.ConversationID) || !validSHA(row.ClaimSHA256) || !validSHA(row.SeedEvidenceSHA256) || !validUUID(row.Epoch) || !validSHA(row.ActivationSHA256) || row.CreatedAt.IsZero() {
		return namespacePrincipals{}, ErrConflict
	}
	return row, nil
}

func (s *Store) readNamespacePrincipals(ctx context.Context, cid string) (NamespaceOriginDescriptor, error) {
	origin, claimSHA, epoch, activation, created, err := s.namespaceClaimIdentity(ctx, cid)
	if err != nil {
		return NamespaceOriginDescriptor{}, err
	}
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespacePrincipalsID(cid)}).Decode(&raw); err != nil {
		return NamespaceOriginDescriptor{}, errors.Join(ErrUnprepared, err)
	}
	row, err := decodeNamespacePrincipals(raw)
	if err != nil {
		return NamespaceOriginDescriptor{}, err
	}
	d := NamespaceOriginDescriptor{CID: cid, Kind: row.OriginKind, GroupID: origin.GroupID, SHA256: row.OriginSHA256, OriginIDs: append([]string(nil), row.OriginIDs...)}
	if err := validateNamespaceDescriptor(d); err != nil {
		return NamespaceOriginDescriptor{}, err
	}
	if row.ConversationID != cid || d.Kind != origin.Kind || d.SHA256 != origin.SHA256 || row.ClaimSHA256 != claimSHA || row.Epoch != epoch || row.ActivationSHA256 != activation || !row.CreatedAt.Equal(created) {
		return NamespaceOriginDescriptor{}, ErrConflict
	}
	return d, nil
}

// Only a real same-TX writer/importer calls this after recording the origin
// claim and seeded counter. A prior immutable companion is inspected rather
// than overwritten, including after activation changes.
func (s *Store) recordNamespacePrincipals(ctx context.Context, d NamespaceOriginDescriptor) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if err := validateNamespaceDescriptor(d); err != nil {
		return err
	}
	origin, claimSHA, epoch, activation, created, err := s.namespaceClaimIdentity(ctx, d.CID)
	if err != nil {
		return err
	}
	if origin.Kind != d.Kind || origin.SHA256 != d.SHA256 || origin.GroupID != d.GroupID {
		return ErrConflict
	}
	prior, err := s.readNamespacePrincipals(ctx, d.CID)
	if err == nil {
		if prior.Kind != d.Kind || prior.SHA256 != d.SHA256 {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	if err := s.requireNamespaceCounter(ctx, d.CID); err != nil {
		return err
	}
	var current counter
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", d.CID)}).Decode(&current); err != nil {
		return err
	}
	row := namespacePrincipals{ID: namespacePrincipalsID(d.CID), Kind: "permanent-origin-principals-v1", ConversationID: d.CID, OriginKind: d.Kind, OriginSHA256: d.SHA256, OriginIDs: append([]string(nil), d.OriginIDs...), ClaimSHA256: claimSHA, Epoch: epoch, ActivationSHA256: activation, SeedEvidenceSHA256: current.SeedEvidenceSHA, CreatedAt: created}
	_, err = s.db.Collection(Sequences).InsertOne(ctx, row)
	return err
}
