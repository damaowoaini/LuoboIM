//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
)

// Facts contain no message Content/AttachedInfo/profile values. These are
// factual source atoms for a bounded private materializer, never reader grants.
// Complete current roster can admit only prospective head+1; calendar-only
// membership cannot certify a historical n_/sg_ sequence bound.
type historicalSegmentLocator struct {
	Epoch        string `json:"epoch,omitempty"`
	HeaderSHA256 string `json:"header_sha256,omitempty"`
	Kind         string `json:"kind"`
	ID           string `json:"id"`
}
type HistoricalFact struct {
	Kind          string                                     `json:"kind"`
	SegmentSource *historicalSegmentLocator                  `json:"segment_source,omitempty"`
	CID           string                                     `json:"cid,omitempty"`
	GroupID       string                                     `json:"group_id,omitempty"`
	OwnerID       string                                     `json:"owner_id,omitempty"`
	Origin        *namespaceimport.NamespaceOriginDescriptor `json:"origin,omitempty"`
	Claim         *HistoricalClaim                           `json:"claim,omitempty"`
	Principals    *HistoricalPrincipals                      `json:"principals,omitempty"`
	Sequence      int64                                      `json:"sequence,omitempty"`
	SlotIndex     int64                                      `json:"slot_index,omitempty"`
	Head          *int64                                     `json:"head,omitempty"`
	Floor         *int64                                     `json:"floor,omitempty"`
	Window        *namespaceimport.OwnerWindow               `json:"window,omitempty"`
	GroupStatus   *int32                                     `json:"group_status,omitempty"`
	Role          *int32                                     `json:"role,omitempty"`
	LastLeftAt    *time.Time                                 `json:"last_left_at,omitempty"`
	Source        namespaceimport.SourceRef                  `json:"source"`
	Reason        string                                     `json:"reason,omitempty"`
}
type DescribeHistoricalNamespace func(*sdkws.MsgData) (namespaceimport.NamespaceOriginDescriptor, error)

func rawFactRef(row namespaceinventory.ImportRawRow) namespaceimport.SourceRef {
	object, record, position := row.Reference()
	return namespaceimport.SourceRef{ObjectSHA256: object, RecordSHA256: record, Kind: row.Source(), Position: position}
}

// DecodeHistoricalRow accepts only the snapshot's private read result. Later
// validation still resolves each SourceRef through the original RawRoot; this
// decoder does not certify the completeness of a set by itself.
func DecodeHistoricalRow(row namespaceinventory.ImportRawRow, describe DescribeHistoricalNamespace) ([]HistoricalFact, error) {
	if describe == nil {
		return nil, ErrOwnedEvidence
	}
	ref := rawFactRef(row)
	if mongo, ok := row.Mongo(); ok {
		return decodeHistoricalMongo(mongo, ref, describe)
	}
	if redis, ok := row.Redis(); ok {
		if !strings.HasPrefix(redis.Key, "MAX_SEQ:") {
			return nil, nil
		}
		cid := strings.TrimPrefix(redis.Key, "MAX_SEQ:")
		// Redis dump/head identity was verified by the two-pass source. RawHead is
		// parsed without normalizing a malformed/negative/overflow value to zero.
		var value json.Number = json.Number(redis.RawHead)
		n, e := value.Int64()
		if redis.Type != "string" || redis.PTTL != -1 || cid == "" || e != nil || n < 0 || n >= math.MaxInt64 || strconv.FormatInt(n, 10) != redis.RawHead {
			return []HistoricalFact{{Kind: "namespace-integrity-gap", CID: cid, Source: ref, Reason: "invalid-or-expiring-redis-head"}}, nil
		}
		return []HistoricalFact{{Kind: "raw-redis-head", CID: cid, Head: &n, Source: ref}}, nil
	}
	return nil, ErrOwnedEvidence
}
func decodeHistoricalMongo(row namespaceinventory.MongoRow, ref namespaceimport.SourceRef, describe DescribeHistoricalNamespace) ([]HistoricalFact, error) {
	observed := namespaceinventory.InspectImportMongo(row)
	for _, reason := range observed.Reasons {
		if reason == "malformed-or-duplicate-BSON" {
			return []HistoricalFact{{Kind: "unresolved-source-row", Source: ref, Reason: reason}}, nil
		}
	}
	raw := bson.Raw(row.Raw)
	bad := func(reason string) []HistoricalFact {
		cid := observed.CID
		if cid == "" {
			if value, ok := raw.Lookup("conversation_id").StringValueOK(); ok && historicalIdentity(value) {
				cid = value
			}
		}
		return []HistoricalFact{{Kind: "unresolved-source-row", CID: cid, Source: ref, Reason: reason}}
	}
	if row.Collection == "message_delivery_receipts_v1" {
		kind, _ := raw.Lookup("kind").StringValueOK()
		if kind == "member-notice-entitlement-v1" || kind == "namespace-import-boundary-v1" {
			locator, e := decodeHistoricalSegmentLocator(raw, ref)
			if e != nil {
				return bad("invalid-original-history-locator"), nil
			}
			if kind == "namespace-import-boundary-v1" {
				return []HistoricalFact{{Kind: "original-prospective-history-needs-lineage", CID: locator.CID, OwnerID: locator.OwnerID, Source: ref, Reason: "immutable-imported-base-needs-original-signed-page"}, locator}, nil
			}
			return []HistoricalFact{locator}, nil
		}
	}
	switch row.Collection {
	case "msg":
		var doc table.MsgDocModel
		if bson.Unmarshal(raw, &doc) != nil || observed.CID == "" {
			return bad("invalid-original-message-coordinate"), nil
		}
		out := make([]HistoricalFact, 0, len(doc.Msg))
		for at, slot := range doc.Msg {
			if slot == nil {
				continue
			}
			if slot.Msg == nil {
				out = append(out, HistoricalFact{Kind: "erased-original-slot", CID: observed.CID, SlotIndex: int64(at), Source: ref, Reason: "origin-may-be-missing"})
				continue
			}
			m := slot.Msg
			wire := &sdkws.MsgData{SendID: m.SendID, RecvID: m.RecvID, GroupID: m.GroupID, SessionType: m.SessionType, Options: m.Options}
			d, e := describe(wire)
			if e != nil || d.CID != observed.CID || m.Seq <= 0 || m.Seq >= math.MaxInt64 || (table.MsgDocModel{}).GetDocID(d.CID, m.Seq) != doc.DocID || (table.MsgDocModel{}).GetMsgIndex(m.Seq) != int64(at) {
				out = append(out, HistoricalFact{Kind: "namespace-integrity-gap", CID: observed.CID, Source: ref, Reason: "typed-original-message-coordinate-mismatch"})
				continue
			}
			d.OriginIDs = append([]string(nil), d.OriginIDs...)
			out = append(out, HistoricalFact{Kind: "retained-original-origin", CID: d.CID, GroupID: d.GroupID, Origin: &d, Sequence: m.Seq, Source: ref})
			if slot.GlobalDeleted {
				out = append(out, HistoricalFact{Kind: "erased-original-slot", CID: d.CID, SlotIndex: int64(at), Source: ref, Reason: "original-origin-retained-but-earlier-reuse-not-disproved"})
			}
		}
		return out, nil
	case "group":
		var group relation.GroupModel
		if bson.Unmarshal(raw, &group) != nil || group.GroupID == "" {
			return bad("invalid-current-group"), nil
		}
		status, ok := raw.Lookup("status").Int32OK()
		if !ok || status != constant.GroupOk && status != constant.GroupStatusMuted && status != constant.GroupStatusDismissed {
			return bad("unknown-current-group-status"), nil
		}
		return []HistoricalFact{{Kind: "held-current-group", GroupID: group.GroupID, GroupStatus: &status, Source: ref}}, nil
	case "group_member":
		var member relation.GroupMemberModel
		if bson.Unmarshal(raw, &member) != nil || member.GroupID == "" || member.UserID == "" {
			return bad("invalid-current-member"), nil
		}
		role, ok := raw.Lookup("role_level").Int32OK()
		if !ok || role != constant.GroupOwner && role != constant.GroupAdmin && role != constant.GroupOrdinaryUsers {
			return bad("invalid-current-member-role"), nil
		}
		return []HistoricalFact{{Kind: "held-current-member", GroupID: member.GroupID, OwnerID: member.UserID, Role: &role, Source: ref}}, nil
	case "group_membership_history":
		gid, gok := raw.Lookup("group_id").StringValueOK()
		uid, uok := raw.Lookup("user_id").StringValueOK()
		if !gok || !uok || gid == "" || uid == "" {
			return bad("invalid-original-membership-calendar"), nil
		}
		var history struct {
			Left *time.Time `bson:"last_left_at"`
		}
		if bson.Unmarshal(raw, &history) != nil {
			return bad("invalid-original-membership-calendar"), nil
		}
		return []HistoricalFact{{Kind: "original-membership-calendar-only", GroupID: gid, OwnerID: uid, LastLeftAt: history.Left, Source: ref, Reason: "no-domain-specific-sequence-bound"}}, nil
	case "message_visibility_floors":
		cid, cok := raw.Lookup("conversation_id").StringValueOK()
		uid, uok := raw.Lookup("user_id").StringValueOK()
		minimum, mok := raw.Lookup("min_seq").Int64OK()
		if !cok || cid == "" || !uok || !mok || minimum < 0 || minimum >= math.MaxInt64 {
			return bad("invalid-original-visibility-floor"), nil
		}
		return []HistoricalFact{{Kind: "original-visibility-floor", CID: cid, OwnerID: uid, Floor: &minimum, Source: ref}}, nil
	case "message_delivery_sequences_v1":
		cid, cok := raw.Lookup("conversation_id").StringValueOK()
		kind, kok := raw.Lookup("kind").StringValueOK()
		if observed.RawHead != nil {
			n := *observed.RawHead
			return []HistoricalFact{{Kind: "raw-mongo-head", CID: observed.CID, Head: &n, Source: ref}}, nil
		}
		if !kok {
			if raw.Lookup("maximum").Type != 0 {
				return bad("unrecognized-counter-shaped-row"), nil
			}
			return nil, nil
		}
		if !cok {
			switch kind {
			case "permanent-origin-principals-v1", "permanent-message-origin-v1", "permanent-historical-namespace-v1", "membership-window-v1", "direct-namespace-window-v1", "namespace-owner-prospective-v1":
				return bad("typed-namespace-row-missing-CID"), nil
			}
		}
		if kind == "namespace-owner-prospective-v1" {
			uid, ok := raw.Lookup("user_id").StringValueOK()
			if !ok || !historicalIdentity(uid) || !cok || !historicalIdentity(cid) {
				return bad("invalid-original-prospective-owner-identity"), nil
			}
			// This row may carry an actual historical revocation and a second proven
			// interval. Until its complete original receipt/page lineage is carried by
			// the interval protocol, HOLD this owner's old base as well. Ignoring the
			// row could resurrect a revoked base or erase an already lawful segment.
			locator, e := decodeHistoricalSegmentLocator(raw, ref)
			if e != nil {
				return bad("invalid-original-history-locator"), nil
			}
			return []HistoricalFact{{Kind: "original-prospective-history-needs-lineage", CID: cid, OwnerID: uid, Source: ref, Reason: "prior-prospective-and-revocation-require-original-receipt-lineage"}, locator}, nil
		}
		if kind == "namespace-owner-live-history-v1" || kind == "namespace-owner-history-admission-v1" || kind == "namespace-owner-imported-history-v1" {
			locator, e := decodeHistoricalSegmentLocator(raw, ref)
			if e != nil {
				return bad("invalid-original-history-locator"), nil
			}
			return []HistoricalFact{{Kind: "original-prospective-history-needs-lineage", CID: locator.CID, OwnerID: locator.OwnerID, Source: ref, Reason: "prior-separate-history-interval-needs-original-chain"}, locator}, nil
		}
		if kind == "namespace-owner-historical-floor-v1" {
			uid, uok := raw.Lookup("user_id").StringValueOK()
			if !cok || !uok || !historicalIdentity(cid) || !historicalIdentity(uid) {
				return bad("invalid-original-history-revocation-identity"), nil
			}
			return []HistoricalFact{{Kind: "original-prospective-history-needs-lineage", CID: cid, OwnerID: uid, Source: ref, Reason: "prior-history-revocation-needs-original-generation-proof"}}, nil
		}
		if kind == "membership-window-v1" || kind == "direct-namespace-window-v1" || kind == "direct-namespace-new-owner-v1" || kind == "direct-namespace-held-owner-v1" {
			fact, e := decodeHistoricalOwnerWindow(raw, ref, describe)
			if e != nil {
				return bad("invalid-original-owner-window"), nil
			}
			return []HistoricalFact{fact}, nil
		}
		switch kind {
		case "permanent-origin-principals-v1":
			value, e := decodeHistoricalPrincipals(raw, describe)
			if e != nil {
				return bad("invalid-original-principals"), nil
			}
			return []HistoricalFact{{Kind: "original-permanent-principals", CID: value.Origin.CID, GroupID: value.Origin.GroupID, Origin: &value.Origin, Principals: &value, Source: ref}}, nil
		case "permanent-message-origin-v1", "permanent-new-group-namespace-v1", "permanent-historical-namespace-v1":
			value, e := decodeHistoricalClaim(raw, describe)
			if e != nil {
				return bad("invalid-original-permanent-claim"), nil
			}
			return []HistoricalFact{{Kind: "original-permanent-claim", CID: value.CID, GroupID: value.GroupID, Claim: &value, Source: ref}}, nil
		case "permanent-historical-group-origin-v1":
			value, e := decodeHistoricalGroupReservation(raw)
			if e != nil {
				return bad("invalid-original-group-reservation"), nil
			}
			return []HistoricalFact{{Kind: "original-permanent-claim", GroupID: value.GroupID, Claim: &value, Source: ref}}, nil
		}
		if raw.Lookup("maximum").Type != 0 {
			return bad("unrecognized-counter-shaped-row"), nil
		}
	}
	return nil, nil
}

// Bounded source step: each atom is visited before loading the next source
// row. A materializer persists its raw cursor and per-row atom position; replay
// after an interrupted batch reuses exact atom keys and never recaptures a cut.
func VisitHistoricalFacts(ctx context.Context, snapshot *namespaceinventory.ImportSnapshot, live namespaceinventory.PageSource, cursor string, rows int, describe DescribeHistoricalNamespace, visit func(HistoricalFact) error) (next string, done bool, err error) {
	if snapshot == nil || live == nil || describe == nil || visit == nil {
		return "", false, ErrOwnedEvidence
	}
	raw, next, done, e := snapshot.ReadRows(ctx, live, cursor, rows)
	if e != nil {
		return "", false, e
	}
	for _, row := range raw {
		values, e := DecodeHistoricalRow(row, describe)
		if e != nil {
			return "", false, e
		}
		for _, fact := range values {
			if e := visit(fact); e != nil {
				return "", false, e
			}
		}
	}
	return next, done, nil
}

// This is only an ORIGINAL typed locator. CID/owner/epoch/_id are taken from
// their actual BSON fields; no interval, role, trusted key or grant is inferred.
// The separately injected MD producer must verify the complete original chain.
func decodeHistoricalSegmentLocator(raw bson.Raw, ref namespaceimport.SourceRef) (HistoricalFact, error) {
	get := func(k string) (string, bool) { return raw.Lookup(k).StringValueOK() }
	id, iok := get("_id")
	kind, kok := get("kind")
	epoch, eok := get("epoch")
	cid, cok := get("conversation_id")
	owner, ook := get("user_id")
	if !iok || !kok || !cok || !ook || !historicalIdentity(id) || !historicalIdentity(cid) || !historicalIdentity(owner) {
		return HistoricalFact{}, ErrOwnedEvidence
	}
	locator := historicalSegmentLocator{Kind: kind, ID: id}
	if kind == "namespace-import-boundary-v1" {
		// This actual original schema has NO epoch field. Only the concrete verifier
		// may resolve the original header SHA through explicit selected source trust.
		header, ok := get("header_sha256")
		if !ok || !isSHA(header) || eok || raw.Lookup("epoch").Type != 0 {
			return HistoricalFact{}, ErrOwnedEvidence
		}
		locator.HeaderSHA256 = header
	} else {
		if !eok {
			return HistoricalFact{}, ErrOwnedEvidence
		}
		u, e := uuid.Parse(epoch)
		if e != nil || u == uuid.Nil || u.String() != epoch {
			return HistoricalFact{}, ErrOwnedEvidence
		}
		locator.Epoch = epoch
		switch kind {
		case "namespace-owner-prospective-v1", "namespace-owner-live-history-v1", "namespace-owner-history-admission-v1", "namespace-owner-imported-history-v1", "member-notice-entitlement-v1":
		default:
			return HistoricalFact{}, ErrOwnedEvidence
		}
	}
	return HistoricalFact{Kind: "original-history-segment-source", CID: cid, OwnerID: owner, Source: ref, SegmentSource: &locator}, nil
}
