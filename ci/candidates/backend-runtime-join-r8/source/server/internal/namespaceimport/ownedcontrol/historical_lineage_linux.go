//go:build linux

package ownedcontrol

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// These factual decoders retain the exact existing typed claim canonical JSON
// identity. They do not create claims, authorize an owner or choose a collision
// winner. The real DescribeNamespace callback supplies every wire CID mapping.
type HistoricalClaim struct {
	ID                             string    `json:"id"`
	Kind                           string    `json:"kind"`
	CID                            string    `json:"cid"`
	GroupID                        string    `json:"group_id,omitempty"`
	OriginKind                     string    `json:"origin_kind,omitempty"`
	OriginSHA256                   string    `json:"origin_sha256"`
	CanonicalSHA256                string    `json:"canonical_sha256"`
	Epoch                          string    `json:"epoch"`
	ActivationSHA256               string    `json:"activation_sha256"`
	CreatedAt                      time.Time `json:"created_at"`
	RequiredGroupReservationSHA256 string    `json:"required_group_reservation_sha256,omitempty"`
}
type HistoricalPrincipals struct {
	ID                 string                                    `json:"id"`
	Origin             namespaceimport.NamespaceOriginDescriptor `json:"origin"`
	ClaimSHA256        string                                    `json:"claim_sha256"`
	Epoch              string                                    `json:"epoch"`
	ActivationSHA256   string                                    `json:"activation_sha256"`
	SeedEvidenceSHA256 string                                    `json:"seed_evidence_sha256"`
	CreatedAt          time.Time                                 `json:"created_at"`
}

var historicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func historicalUUIDValid(v string) bool {
	return historicalUUID.MatchString(v) && v != "00000000-0000-0000-0000-000000000000"
}
func historicalIdentity(v string) bool {
	return v != "" && len(v) <= 512 && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\x00\r\n")
}
func historicalJSONSHA(v any) string { b, _ := json.Marshal(v); return sha(b) }
func historicalExact(raw bson.Raw, ss, ll, bb []string, date string, arrays []string) bool {
	elems, e := raw.Elements()
	if e != nil || len(elems) != len(ss)+len(ll)+len(bb)+len(arrays)+func() int {
		if date != "" {
			return 1
		}
		return 0
	}() {
		return false
	}
	seen := map[string]bool{}
	for _, el := range elems {
		if seen[el.Key()] {
			return false
		}
		seen[el.Key()] = true
	}
	for _, s := range ss {
		if _, ok := raw.Lookup(s).StringValueOK(); !ok {
			return false
		}
	}
	for _, s := range ll {
		if _, ok := raw.Lookup(s).Int64OK(); !ok {
			return false
		}
	}
	for _, s := range bb {
		if _, ok := raw.Lookup(s).BooleanOK(); !ok {
			return false
		}
	}
	for _, s := range arrays {
		if _, ok := raw.Lookup(s).ArrayOK(); !ok {
			return false
		}
	}
	if date != "" {
		if _, ok := raw.Lookup(date).DateTimeOK(); !ok {
			return false
		}
	}
	return true
}

// Descriptor reconstruction varies only real protocol SessionType/Options;
// it never strips/splits a CID to invent origin IDs or group membership.
func historicalDescriptor(cid, kind, gid string, ids []string, describe DescribeHistoricalNamespace) (namespaceimport.NamespaceOriginDescriptor, error) {
	if !historicalIdentity(cid) {
		return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
	}
	var sessions []int32
	switch kind {
	case "group":
		if len(ids) != 1 || ids[0] != gid || !historicalIdentity(gid) {
			return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
		}
		sessions = []int32{constant.GroupChatType, constant.SuperGroupChatType}
	case "single":
		if len(ids) != 2 || gid != "" || ids[0] > ids[1] {
			return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
		}
		sessions = []int32{constant.SingleChatType}
	case "server-notification":
		if len(ids) != 2 || gid != "" {
			return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
		}
		sessions = []int32{constant.NotificationChatType}
	default:
		return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
	}
	for _, id := range ids {
		if !historicalIdentity(id) {
			return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
		}
	}
	for _, session := range sessions {
		for _, normal := range []bool{true, false} {
			m := &sdkws.MsgData{SessionType: session, Options: map[string]bool{constant.IsNotNotification: normal}}
			if kind == "group" {
				m.SendID = gid
				m.RecvID = gid
				m.GroupID = gid
			} else {
				m.SendID = ids[0]
				m.RecvID = ids[1]
			}
			d, e := describe(m)
			if e == nil && d.CID == cid && d.Kind == kind && d.GroupID == gid && historicalJSONSHA(d.OriginIDs) == historicalJSONSHA(ids) {
				return d, nil
			}
		}
	}
	return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
}

// Field ordering/exported names below match the existing Store typed structs,
// whose json.Marshal identity binds every retained claim field. Source review
// pins those original definitions; these are data decoders, not a second CID
// algorithm or an opportunity to reinterpret unknown Sequence row kinds.
type originalFreshClaim struct {
	ID               string    `bson:"_id"`
	Kind             string    `bson:"kind"`
	ConversationID   string    `bson:"conversation_id"`
	GroupID          string    `bson:"group_id"`
	Branch           string    `bson:"branch"`
	Epoch            string    `bson:"epoch"`
	ActivationSHA256 string    `bson:"activation_sha256"`
	CommandID        string    `bson:"command_id"`
	InputSHA256      string    `bson:"input_sha256"`
	CutoverSHA256    string    `bson:"cutover_sha256"`
	CreatedAt        time.Time `bson:"created_at"`
}
type originalDirectClaim struct {
	ID                 string    `bson:"_id"`
	Kind               string    `bson:"kind"`
	ConversationID     string    `bson:"conversation_id"`
	OriginKind         string    `bson:"origin_kind"`
	OriginSHA256       string    `bson:"origin_sha256"`
	Epoch              string    `bson:"created_epoch"`
	ActivationSHA256   string    `bson:"created_activation_sha256"`
	SeedEvidenceSHA256 string    `bson:"seed_evidence_sha256"`
	CreatedAt          time.Time `bson:"created_at"`
}
type originalImportClaim struct {
	ID                 string    `bson:"_id"`
	Kind               string    `bson:"kind"`
	ConversationID     string    `bson:"conversation_id"`
	OriginKind         string    `bson:"origin_kind"`
	OriginSHA256       string    `bson:"origin_sha256"`
	GroupID            string    `bson:"group_id"`
	Epoch              string    `bson:"created_epoch"`
	ActivationSHA256   string    `bson:"created_activation_sha256"`
	MigrationID        string    `bson:"migration_id"`
	HeaderSHA256       string    `bson:"header_sha256"`
	InventorySHA256    string    `bson:"inventory_sha256"`
	SeedEvidenceSHA256 string    `bson:"seed_evidence_sha256"`
	CreatedAt          time.Time `bson:"created_at"`
}

func decodeHistoricalClaim(raw bson.Raw, describe DescribeHistoricalNamespace) (HistoricalClaim, error) {
	kind, _ := raw.Lookup("kind").StringValueOK()
	var out HistoricalClaim
	switch kind {
	case "permanent-new-group-namespace-v1":
		if !historicalExact(raw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at", nil) {
			return out, ErrOwnedEvidence
		}
		var row originalFreshClaim
		if bson.Unmarshal(raw, &row) != nil || !historicalUUIDValid(row.Epoch) || !historicalUUIDValid(row.CommandID) || !isSHA(row.ActivationSHA256) || !isSHA(row.InputSHA256) || !isSHA(row.CutoverSHA256) || row.CreatedAt.IsZero() || !historicalIdentity(row.GroupID) {
			return out, ErrOwnedEvidence
		}
		if row.Branch == "group-origin" {
			if row.ConversationID != "" || row.ID != "permanent-group-origin:"+sha([]byte(row.GroupID)) {
				return out, ErrOwnedEvidence
			}
			out.GroupID = row.GroupID
		} else {
			if row.Branch != "normal" && row.Branch != "notification" || row.ID != "permanent-cid:"+sha([]byte(row.ConversationID)) {
				return out, ErrOwnedEvidence
			}
			d, e := historicalDescriptor(row.ConversationID, "group", row.GroupID, []string{row.GroupID}, describe)
			if e != nil {
				return out, e
			}
			out.CID = d.CID
			out.GroupID = d.GroupID
			// Normal/notification must be the exact protocol branch, not merely one of
			// the group's CID variants. No simple "n_"+normalCID concatenation occurs.
			message := &sdkws.MsgData{SendID: row.GroupID, RecvID: row.GroupID, GroupID: row.GroupID, SessionType: constant.SuperGroupChatType, Options: map[string]bool{constant.IsNotNotification: row.Branch == "normal"}}
			branch, e := describe(message)
			if e != nil || branch.CID != row.ConversationID {
				return out, ErrOwnedEvidence
			}
		}
		out.ID = row.ID
		out.Kind = kind
		out.OriginKind = "group"
		out.OriginSHA256 = historicalJSONSHA([]any{"group", []string{row.GroupID}})
		out.CanonicalSHA256 = historicalJSONSHA(row)
		out.Epoch = row.Epoch
		out.ActivationSHA256 = row.ActivationSHA256
		out.CreatedAt = row.CreatedAt
		origin := row
		origin.ID = "permanent-group-origin:" + sha([]byte(row.GroupID))
		origin.ConversationID = ""
		origin.Branch = "group-origin"
		out.RequiredGroupReservationSHA256 = historicalJSONSHA(origin)
	case "permanent-message-origin-v1":
		if !historicalExact(raw, []string{"_id", "kind", "conversation_id", "origin_kind", "origin_sha256", "created_epoch", "created_activation_sha256", "seed_evidence_sha256"}, nil, nil, "created_at", nil) {
			return out, ErrOwnedEvidence
		}
		var row originalDirectClaim
		if bson.Unmarshal(raw, &row) != nil || row.ID != "permanent-cid:"+sha([]byte(row.ConversationID)) || !historicalIdentity(row.ConversationID) || row.OriginKind != "single" && row.OriginKind != "server-notification" || !isSHA(row.OriginSHA256) || !historicalUUIDValid(row.Epoch) || !isSHA(row.ActivationSHA256) || !isSHA(row.SeedEvidenceSHA256) || row.CreatedAt.IsZero() {
			return out, ErrOwnedEvidence
		}
		out = HistoricalClaim{ID: row.ID, Kind: kind, CID: row.ConversationID, OriginKind: row.OriginKind, OriginSHA256: row.OriginSHA256, CanonicalSHA256: historicalJSONSHA(row), Epoch: row.Epoch, ActivationSHA256: row.ActivationSHA256, CreatedAt: row.CreatedAt}
	case "permanent-historical-namespace-v1":
		if !historicalExact(raw, []string{"_id", "kind", "conversation_id", "origin_kind", "origin_sha256", "group_id", "created_epoch", "created_activation_sha256", "migration_id", "header_sha256", "inventory_sha256", "seed_evidence_sha256"}, nil, nil, "created_at", nil) {
			return out, ErrOwnedEvidence
		}
		var row originalImportClaim
		if bson.Unmarshal(raw, &row) != nil || row.ID != "permanent-cid:"+sha([]byte(row.ConversationID)) || !historicalIdentity(row.ConversationID) || !historicalUUIDValid(row.Epoch) || !historicalUUIDValid(row.MigrationID) || !isSHA(row.ActivationSHA256) || !isSHA(row.OriginSHA256) || !isSHA(row.HeaderSHA256) || !isSHA(row.InventorySHA256) || !isSHA(row.SeedEvidenceSHA256) || row.CreatedAt.IsZero() {
			return out, ErrOwnedEvidence
		}
		if row.OriginKind == "group" {
			if _, e := historicalDescriptor(row.ConversationID, row.OriginKind, row.GroupID, []string{row.GroupID}, describe); e != nil {
				return out, e
			}
		} else if row.GroupID != "" || row.OriginKind != "single" && row.OriginKind != "server-notification" {
			return out, ErrOwnedEvidence
		}
		out = HistoricalClaim{ID: row.ID, Kind: kind, CID: row.ConversationID, GroupID: row.GroupID, OriginKind: row.OriginKind, OriginSHA256: row.OriginSHA256, CanonicalSHA256: historicalJSONSHA(row), Epoch: row.Epoch, ActivationSHA256: row.ActivationSHA256, CreatedAt: row.CreatedAt}
	default:
		return out, ErrOwnedEvidence
	}
	return out, nil
}
func decodeHistoricalPrincipals(raw bson.Raw, describe DescribeHistoricalNamespace) (HistoricalPrincipals, error) {
	var out HistoricalPrincipals
	if !historicalExact(raw, []string{"_id", "kind", "conversation_id", "origin_kind", "origin_sha256", "claim_sha256", "created_epoch", "created_activation_sha256", "seed_evidence_sha256"}, nil, nil, "created_at", []string{"origin_ids"}) {
		return out, ErrOwnedEvidence
	}
	var row struct {
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
	if bson.Unmarshal(raw, &row) != nil || row.Kind != "permanent-origin-principals-v1" || row.ID != "permanent-origin-principals:"+sha([]byte(row.ConversationID)) || !isSHA(row.ClaimSHA256) || !isSHA(row.SeedEvidenceSHA256) || !historicalUUIDValid(row.Epoch) || !isSHA(row.ActivationSHA256) || row.CreatedAt.IsZero() {
		return out, ErrOwnedEvidence
	}
	gid := ""
	if row.OriginKind == "group" && len(row.OriginIDs) == 1 {
		gid = row.OriginIDs[0]
	}
	d, e := historicalDescriptor(row.ConversationID, row.OriginKind, gid, row.OriginIDs, describe)
	if e != nil || d.SHA256 != row.OriginSHA256 {
		return out, ErrOwnedEvidence
	}
	return HistoricalPrincipals{ID: row.ID, Origin: d, ClaimSHA256: row.ClaimSHA256, Epoch: row.Epoch, ActivationSHA256: row.ActivationSHA256, SeedEvidenceSHA256: row.SeedEvidenceSHA256, CreatedAt: row.CreatedAt}, nil
}
func historicalSequenceKey(epoch, kind, id string) string {
	return historicalJSONSHA([]string{epoch, kind, id})
}
func decodeHistoricalOwnerWindow(raw bson.Raw, ref namespaceimport.SourceRef, describe DescribeHistoricalNamespace) (HistoricalFact, error) {
	var out HistoricalFact
	kind, _ := raw.Lookup("kind").StringValueOK()
	var w namespaceimport.OwnerWindow
	if kind == "membership-window-v1" {
		if !historicalExact(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"lower", "upper", "revision", "group_revision"}, []string{"closed"}, "", nil) {
			return out, ErrOwnedEvidence
		}
		var row struct {
			ID            string `bson:"_id"`
			Kind          string `bson:"kind"`
			Epoch         string `bson:"epoch"`
			CID           string `bson:"conversation_id"`
			GID           string `bson:"group_id"`
			UID           string `bson:"user_id"`
			Command       string `bson:"last_command"`
			Seed          string `bson:"seed_evidence_sha256"`
			Lower         int64  `bson:"lower"`
			Upper         int64  `bson:"upper"`
			Revision      int64  `bson:"revision"`
			GroupRevision int64  `bson:"group_revision"`
			Closed        bool   `bson:"closed"`
		}
		if bson.Unmarshal(raw, &row) != nil || !historicalUUIDValid(row.Epoch) || !historicalIdentity(row.UID) || row.ID != historicalSequenceKey(row.Epoch, "membership-window", row.CID+"\x00"+row.UID) || row.Revision < 1 || row.Revision >= math.MaxInt64 || row.GroupRevision < 1 || row.GroupRevision >= math.MaxInt64 || !isSHA(row.Seed) || row.Closed && !historicalUUIDValid(row.Command) || !row.Closed && row.Command != "" && !historicalUUIDValid(row.Command) {
			return out, ErrOwnedEvidence
		}
		d, e := historicalDescriptor(row.CID, "group", row.GID, []string{row.GID}, describe)
		if e != nil {
			return out, e
		}
		w = namespaceimport.OwnerWindow{CID: row.CID, GroupID: row.GID, OwnerID: row.UID, OriginSHA256: d.SHA256, Lower: row.Lower, Upper: row.Upper, Closed: row.Closed, Evidence: []namespaceimport.SourceRef{ref}}
	} else if kind == "direct-namespace-window-v1" || kind == "direct-namespace-new-owner-v1" || kind == "direct-namespace-held-owner-v1" {
		if !historicalExact(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "header_sha256", "boundary_sha256"}, []string{"lower", "upper"}, []string{"closed"}, "", nil) {
			return out, ErrOwnedEvidence
		}
		var row struct {
			ID       string `bson:"_id"`
			Epoch    string `bson:"epoch"`
			CID      string `bson:"conversation_id"`
			UID      string `bson:"user_id"`
			Origin   string `bson:"origin_sha256"`
			Header   string `bson:"header_sha256"`
			Boundary string `bson:"boundary_sha256"`
			Lower    int64  `bson:"lower"`
			Upper    int64  `bson:"upper"`
			Closed   bool   `bson:"closed"`
		}
		if bson.Unmarshal(raw, &row) != nil || !historicalUUIDValid(row.Epoch) || !historicalIdentity(row.UID) || row.ID != historicalSequenceKey(row.Epoch, "direct-namespace-window", row.CID+"\x00"+row.UID) || !isSHA(row.Origin) || !isSHA(row.Header) || !isSHA(row.Boundary) {
			return out, ErrOwnedEvidence
		}
		w = namespaceimport.OwnerWindow{CID: row.CID, OwnerID: row.UID, OriginSHA256: row.Origin, Lower: row.Lower, Upper: row.Upper, Closed: row.Closed, Evidence: []namespaceimport.SourceRef{ref}}
	} else {
		return out, ErrOwnedEvidence
	}
	if !historicalIdentity(w.CID) || w.Lower < 0 || w.Upper < 0 || w.Lower >= math.MaxInt64 || w.Upper >= math.MaxInt64 || !w.Closed && w.Upper != 0 {
		return out, ErrOwnedEvidence
	}
	return HistoricalFact{Kind: "original-explicit-owner-window", CID: w.CID, GroupID: w.GroupID, OwnerID: w.OwnerID, Window: &w, Source: ref}, nil
}

func decodeHistoricalGroupReservation(raw bson.Raw) (HistoricalClaim, error) {
	var out HistoricalClaim
	if !historicalExact(raw, []string{"_id", "kind", "group_id", "origin_sha256", "created_epoch", "created_activation_sha256", "migration_id", "header_sha256", "inventory_sha256"}, nil, nil, "created_at", nil) {
		return out, ErrOwnedEvidence
	}
	var row struct {
		ID               string    `bson:"_id"`
		Kind             string    `bson:"kind"`
		GroupID          string    `bson:"group_id"`
		OriginSHA256     string    `bson:"origin_sha256"`
		Epoch            string    `bson:"created_epoch"`
		ActivationSHA256 string    `bson:"created_activation_sha256"`
		MigrationID      string    `bson:"migration_id"`
		HeaderSHA256     string    `bson:"header_sha256"`
		InventorySHA256  string    `bson:"inventory_sha256"`
		CreatedAt        time.Time `bson:"created_at"`
	}
	if bson.Unmarshal(raw, &row) != nil || row.Kind != "permanent-historical-group-origin-v1" || !historicalIdentity(row.GroupID) || row.ID != "permanent-group-origin:"+sha([]byte(row.GroupID)) || row.OriginSHA256 != historicalJSONSHA([]any{"group", []string{row.GroupID}}) || !historicalUUIDValid(row.Epoch) || !historicalUUIDValid(row.MigrationID) || !isSHA(row.ActivationSHA256) || !isSHA(row.HeaderSHA256) || !isSHA(row.InventorySHA256) || row.CreatedAt.IsZero() {
		return out, ErrOwnedEvidence
	}
	return HistoricalClaim{ID: row.ID, Kind: row.Kind, GroupID: row.GroupID, OriginKind: "group", OriginSHA256: row.OriginSHA256, CanonicalSHA256: historicalJSONSHA(row), Epoch: row.Epoch, ActivationSHA256: row.ActivationSHA256, CreatedAt: row.CreatedAt}, nil
}
