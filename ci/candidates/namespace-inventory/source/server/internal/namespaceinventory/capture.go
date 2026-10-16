// Package namespaceinventory captures CURRENT evidence only. It has no apply,
// seed, Ready, writer-capability, message dispatch or migration activation API.
package namespaceinventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

var (
	ErrSource   = errors.New("namespace inventory source unavailable")
	ErrIdentity = errors.New("namespace inventory identity mismatch or restart")
	ErrChanged  = errors.New("namespace inventory changed during capture")
	ErrLimit    = errors.New("namespace inventory bounded capture limit")
	ErrInvalid  = errors.New("invalid namespace inventory document")
	ErrPrivate  = errors.New("namespace inventory private file contract")
)

type Binding struct {
	Tenant          string `json:"tenant"`
	Database        string `json:"database"`
	StorageInstance string `json:"storageInstance"`
	ReplicaSet      string `json:"replicaSet"`
	RedisRunID      string `json:"redisRunID"`
	RedisDB         int    `json:"redisDB"`
}
type Identity struct {
	Binding          Binding `json:"binding"`
	MongoProcess     string  `json:"mongoProcess"`
	MongoTopology    int64   `json:"mongoTopology"`
	RedisReplication string  `json:"redisReplicationID"`
	TenantAttested   bool    `json:"tenantAttested"`
}
type Limits struct {
	Rows, Bytes, EntryBytes, RedisElements int
	Timeout                                time.Duration
}

func DefaultLimits() Limits {
	return Limits{Rows: 10000, Bytes: 64 << 20, EntryBytes: 16 << 20, RedisElements: 4096, Timeout: 120 * time.Second}
}
func (l Limits) valid() bool {
	return l.Rows > 0 && l.Rows <= 10000 && l.Bytes > 0 && l.Bytes <= 64<<20 && l.EntryBytes > 0 && l.EntryBytes <= 16<<20 && l.RedisElements > 0 && l.RedisElements <= 4096 && l.Timeout > 0 && l.Timeout <= 300*time.Second
}
func text(v string) bool {
	if v == "" || len(v) > 512 || !utf8.ValidString(v) || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hexText(v string, n int) bool {
	if len(v) != n || strings.ToLower(v) != v {
		return false
	}
	_, e := hex.DecodeString(v)
	return e == nil
}
func (b Binding) valid() bool {
	id, e := uuid.Parse(b.StorageInstance)
	return text(b.Tenant) && text(b.Database) && e == nil && id.String() == b.StorageInstance && id != uuid.Nil && text(b.ReplicaSet) && hexText(b.RedisRunID, 40) && b.RedisDB >= 0 && b.RedisDB <= 15
}
func (i Identity) valid() bool {
	return i.Binding.valid() && hexText(i.MongoProcess, 24) && i.MongoTopology >= 0 && hexText(i.RedisReplication, 40)
}

type MongoRow struct {
	Collection string `json:"collection"`
	KeySHA     string `json:"keySHA256"`
	Raw        []byte `json:"rawBSON"`
}
type RedisRow struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Dump    []byte `json:"dump"`
	RawHead string `json:"rawHead"`
	PTTL    int64  `json:"pttlMilliseconds"`
}

// Deliberately read-only leaf port. LiveSource below implements real primary
// Mongo and EVAL_RO Redis reads; synthetic tests cannot create a writer grant.
type Source interface {
	Identity(context.Context) (Identity, error)
	Mongo(context.Context, func(MongoRow) error) error
	Redis(context.Context, func(RedisRow) error) error
}
type Origin struct {
	Kind     string `json:"kind"`
	SHA      string `json:"sha256"`
	Evidence string `json:"evidence"`
}
type Observation struct {
	Source      string   `json:"source"`
	CID         string   `json:"cid,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Origins     []Origin `json:"candidateOrigins,omitempty"`
	ObservedSeq int64    `json:"largestObservedMessageSeq,omitempty"`
	RawHead     *int64   `json:"observedRawHead,omitempty"`
	Erased      int      `json:"erasedSlots,omitempty"`
	Empty       int      `json:"emptySlots,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
}
type Snapshot struct {
	Version      int           `json:"version"`
	ID           string        `json:"id"`
	Created      time.Time     `json:"createdAt"`
	State        string        `json:"state"`
	Identity     Identity      `json:"identity"`
	Mongo        []MongoRow    `json:"mongo"`
	Redis        []RedisRow    `json:"redis"`
	Observations []Observation `json:"observations"`
	Reasons      []string      `json:"reasons"`
	SHA          string        `json:"sha256"`
}
type PublicSummary struct {
	State               string         `json:"state"`
	SnapshotSHA         string         `json:"snapshotSHA256"`
	MongoRows           int            `json:"mongoRows"`
	RedisKeys           int            `json:"redisKeys"`
	ErasedSlots         int            `json:"erasedSlots"`
	CandidateCollisions int            `json:"candidateCollisions"`
	Reasons             map[string]int `json:"reasonCounts"`
}

func origin(m *sdkws.MsgData) (Origin, error) {
	if m == nil || !text(m.SendID) || !text(m.RecvID) {
		return Origin{}, ErrInvalid
	}
	kind, ids := "", []string(nil)
	switch m.SessionType {
	case constant.SuperGroupChatType, constant.GroupChatType:
		if !text(m.GroupID) || m.RecvID != m.GroupID {
			return Origin{}, ErrInvalid
		}
		kind, ids = "group", []string{m.GroupID}
	case constant.SingleChatType:
		if m.GroupID != "" {
			return Origin{}, ErrInvalid
		}
		kind, ids = "single", []string{m.SendID, m.RecvID}
		sort.Strings(ids)
	case constant.NotificationChatType:
		if m.GroupID != "" {
			return Origin{}, ErrInvalid
		}
		kind, ids = "server-notification", []string{m.SendID, m.RecvID}
	default:
		return Origin{}, ErrInvalid
	}
	if !text(msgprocessor.GetConversationIDByMsg(m)) {
		return Origin{}, ErrInvalid
	}
	b, _ := json.Marshal([]any{kind, ids})
	return Origin{Kind: kind, SHA: digest(b), Evidence: "retained-wire-fields-current-observation"}, nil
}

// Duplicate BSON keys, including nested message options, are not last-wins
// origin evidence. Malformed data is retained raw with an unresolved reason.
func uniqueBSON(raw bson.Raw, depth int) bool {
	if depth > 32 {
		return false
	}
	fields, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fields {
		fold := strings.ToLower(f.Key())
		if seen[fold] {
			return false
		}
		seen[fold] = true
		v := f.Value()
		if d, ok := v.DocumentOK(); ok && !uniqueBSON(d, depth+1) {
			return false
		}
		if a, ok := v.ArrayOK(); ok && !uniqueBSON(a, depth+1) {
			return false
		}
	}
	return true
}
func classify(row MongoRow) Observation {
	o := Observation{Source: row.Collection + ":" + row.KeySHA}
	if !uniqueBSON(row.Raw, 0) {
		o.Reasons = []string{"malformed-or-duplicate-BSON"}
		return o
	}
	raw := bson.Raw(row.Raw)
	if row.Collection == "msg" {
		var doc table.MsgDocModel
		if bson.Unmarshal(raw, &doc) != nil || !text(doc.DocID) {
			o.Reasons = []string{"malformed-message-document"}
			return o
		}
		// Only separate the model's final numeric block suffix. Never split a
		// CID's underscore tuple into users/group or call this an origin proof.
		at := strings.LastIndexByte(doc.DocID, ':')
		if at < 1 {
			o.Reasons = []string{"invalid-document-coordinate"}
			return o
		}
		block, e := strconv.ParseInt(doc.DocID[at+1:], 10, 64)
		if e != nil || block < 0 {
			o.Reasons = []string{"invalid-document-coordinate"}
			return o
		}
		o.CID = doc.DocID[:at]
		seen := map[string]bool{}
		for i, slot := range doc.Msg {
			if slot == nil {
				o.Empty++
				continue
			}
			if slot.Msg == nil {
				o.Erased++
				continue
			}
			if slot.GlobalDeleted {
				o.Erased++
				o.Reasons = append(o.Reasons, "deleted-slot-retains-original-origin-only")
			}
			m := slot.Msg
			wire := &sdkws.MsgData{SendID: m.SendID, RecvID: m.RecvID, GroupID: m.GroupID, SessionType: m.SessionType, Options: m.Options}
			candidate, e := origin(wire)
			if e != nil || m.Seq <= 0 || m.Seq == math.MaxInt64 || msgprocessor.GetConversationIDByMsg(wire) != o.CID || (table.MsgDocModel{}).GetDocID(o.CID, m.Seq) != doc.DocID || (table.MsgDocModel{}).GetMsgIndex(m.Seq) != int64(i) {
				o.Reasons = append(o.Reasons, "message-origin-or-slot-coordinate-invalid")
				continue
			}
			if !seen[candidate.SHA] {
				seen[candidate.SHA] = true
				o.Origins = append(o.Origins, candidate)
			}
			if m.Seq > o.ObservedSeq {
				o.ObservedSeq = m.Seq
			}
		}
		if o.Erased > 0 {
			o.Reasons = append(o.Reasons, "erased-slot-origin-and-head-incomplete")
		}
		if len(o.Origins) == 0 {
			o.Reasons = append(o.Reasons, "no-retained-message-origin")
		}
		return o
	}
	if row.Collection == "conversation" {
		owner, _ := raw.Lookup("owner_user_id").StringValueOK()
		cid, _ := raw.Lookup("conversation_id").StringValueOK()
		peer, _ := raw.Lookup("user_id").StringValueOK()
		gid, _ := raw.Lookup("group_id").StringValueOK()
		kind, ok := raw.Lookup("conversation_type").Int32OK()
		o.CID, o.Owner = cid, owner
		if !text(cid) || !text(owner) || !ok {
			o.Reasons = []string{"unresolved-current-account-routing"}
			return o
		}
		wire := &sdkws.MsgData{SendID: owner, RecvID: peer, GroupID: gid, SessionType: kind, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(true))}
		if kind == constant.SuperGroupChatType || kind == constant.GroupChatType {
			wire.RecvID = gid
		}
		candidate, e := origin(wire)
		if e == nil && msgprocessor.GetConversationIDByMsg(wire) == cid {
			candidate.Evidence = "current-account-routing-not-historical-origin"
			o.Origins = []Origin{candidate}
		} else {
			o.Reasons = append(o.Reasons, "unresolved-current-account-routing")
		}
		o.Reasons = append(o.Reasons, "current-routing-does-not-prove-nonreuse")
		return o
	}
	if row.Collection == "group" {
		gid, ok := raw.Lookup("group_id").StringValueOK()
		if !ok || !text(gid) {
			o.Reasons = []string{"unresolved-current-group-routing"}
			return o
		}
		wire := &sdkws.MsgData{SendID: gid, RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType}
		candidate, e := origin(wire)
		if e != nil {
			o.Reasons = []string{"unresolved-current-group-routing"}
			return o
		}
		candidate.Evidence = "current-group-row-not-historical-origin"
		o.CID = msgprocessor.GetNotificationConversationIDByMsg(wire)
		o.Origins = []Origin{candidate}
		o.Reasons = []string{"current-routing-does-not-prove-nonreuse"}
		return o
	}
	if cid, ok := raw.Lookup("conversation_id").StringValueOK(); ok {
		o.CID = cid
	}
	if row.Collection == "message_delivery_sequences_v1" {
		// Exact row class, never decode schema/claim/window as a counter.
		kind := raw.Lookup("kind")
		if kind.Type == 0 && text(o.CID) {
			fields, _ := raw.Elements()
			allowed := map[string]bool{"_id": true, "epoch": true, "conversation_id": true, "maximum": true, "revision": true, "seed_maximum": true, "seed_evidence_sha256": true}
			exact := len(fields) == len(allowed)
			for _, f := range fields {
				if !allowed[f.Key()] {
					exact = false
				}
			}
			n, ok := raw.Lookup("maximum").Int64OK()
			seed, sok := raw.Lookup("seed_maximum").Int64OK()
			rev, rok := raw.Lookup("revision").Int64OK()
			if exact && ok && sok && rok && seed >= 0 && n >= seed && n < math.MaxInt64 && rev > 0 && rev < math.MaxInt64 {
				o.RawHead = &n
			} else {
				o.Reasons = append(o.Reasons, "invalid-shared-head")
			}
		} else if name, ok := kind.StringValueOK(); ok && text(o.CID) {
			if name == "permanent-message-origin-v1" {
				k, kok := raw.Lookup("origin_kind").StringValueOK()
				h, hok := raw.Lookup("origin_sha256").StringValueOK()
				if kok && hok && (k == "single" || k == "server-notification") && hexText(h, 64) {
					o.Origins = []Origin{{Kind: k, SHA: h, Evidence: "current-typed-claim-not-historical-attestation"}}
				}
			}
			if name == "permanent-new-group-namespace-v1" {
				gid, gok := raw.Lookup("group_id").StringValueOK()
				branch, bok := raw.Lookup("branch").StringValueOK()
				wire := &sdkws.MsgData{SendID: gid, RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType}
				cid := msgprocessor.GetChatConversationIDByMsg(wire)
				if branch == "notification" {
					cid = msgprocessor.GetNotificationConversationIDByMsg(wire)
				}
				if gok && bok && text(gid) && (branch == "normal" || branch == "notification") && cid == o.CID {
					candidate, e := origin(wire)
					if e == nil {
						candidate.Evidence = "current-typed-claim-not-historical-attestation"
						o.Origins = []Origin{candidate}
					}
				}
			}
		}
	}
	o.Reasons = append(o.Reasons, "metadata-observation-not-origin-or-owner-grant")
	return o
}

func collect(ctx context.Context, s Source, l Limits) ([]MongoRow, []RedisRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	used := 0
	mongoRows := map[string]MongoRow{}
	redisRows := map[string]RedisRow{}
	add := func(size int) error {
		if size > l.EntryBytes || size < 0 || used > l.Bytes-size {
			return ErrLimit
		}
		used += size
		if len(mongoRows)+len(redisRows) >= l.Rows {
			return ErrLimit
		}
		return ctx.Err()
	}
	if err := s.Mongo(ctx, func(r MongoRow) error {
		if !allowedCollection(r.Collection) || !hexText(r.KeySHA, 64) {
			return ErrInvalid
		}
		k := r.Collection + ":" + r.KeySHA
		if old, ok := mongoRows[k]; ok {
			if !bytes.Equal(old.Raw, r.Raw) {
				return ErrChanged
			}
			return nil
		}
		if err := add(len(r.Raw)); err != nil {
			return err
		}
		r.Raw = append([]byte(nil), r.Raw...)
		mongoRows[k] = r
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if err := s.Redis(ctx, func(r RedisRow) error {
		if !allowedKey(r.Key) || !allowedType(r.Type) || r.PTTL < -1 {
			return ErrChanged
		}
		if old, ok := redisRows[r.Key]; ok {
			if old.Type != r.Type || old.RawHead != r.RawHead || !bytes.Equal(old.Dump, r.Dump) {
				return ErrChanged
			}
			return nil
		}
		if err := add(len(r.Key) + len(r.Dump) + len(r.RawHead)); err != nil {
			return err
		}
		r.Dump = append([]byte(nil), r.Dump...)
		redisRows[r.Key] = r
		return nil
	}); err != nil {
		return nil, nil, err
	}
	ms := make([]MongoRow, 0, len(mongoRows))
	rs := make([]RedisRow, 0, len(redisRows))
	for _, r := range mongoRows {
		ms = append(ms, r)
	}
	for _, r := range redisRows {
		rs = append(rs, r)
	}
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Collection == ms[j].Collection {
			return ms[i].KeySHA < ms[j].KeySHA
		}
		return ms[i].Collection < ms[j].Collection
	})
	sort.Slice(rs, func(i, j int) bool { return rs[i].Key < rs[j].Key })
	return ms, rs, nil
}

func Capture(ctx context.Context, s Source, expected Binding, l Limits) (*Snapshot, error) {
	if ctx == nil || s == nil || !expected.valid() || !l.valid() {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	first, err := s.Identity(current)
	if err != nil {
		return nil, err
	}
	if !first.valid() || first.Binding != expected {
		return nil, ErrIdentity
	}
	mongoRows, redisRows, err := collect(current, s, l)
	if err != nil {
		return nil, err
	}
	middle, err := s.Identity(current)
	if err != nil {
		return nil, err
	}
	if middle != first {
		return nil, ErrIdentity
	}
	againM, againR, err := collect(current, s, l)
	if err != nil {
		return nil, err
	}
	last, err := s.Identity(current)
	if err != nil {
		return nil, err
	}
	if last != first {
		return nil, ErrIdentity
	}
	if err := current.Err(); err != nil {
		return nil, err
	}
	a, _ := json.Marshal(mongoRows)
	b, _ := json.Marshal(againM)
	if !bytes.Equal(a, b) || len(redisRows) != len(againR) {
		return nil, ErrChanged
	}
	for i, r := range redisRows {
		later := againR[i]
		if r.Key != later.Key || r.Type != later.Type || r.RawHead != later.RawHead || !bytes.Equal(r.Dump, later.Dump) || (r.PTTL < 0) != (later.PTTL < 0) || later.PTTL > r.PTTL+100 {
			return nil, ErrChanged
		}
	}
	id, e := uuid.NewRandom()
	if e != nil {
		return nil, ErrSource
	}
	snapshot := &Snapshot{Version: 1, ID: id.String(), Created: time.Now().UTC(), State: State, Identity: first, Mongo: mongoRows, Redis: redisRows, Reasons: fixedReasons(first)}
	snapshot.Observations = observations(mongoRows, redisRows)
	snapshot.SHA = snapshotDigest(*snapshot)
	return snapshot, nil
}
func observations(mongoRows []MongoRow, redisRows []RedisRow) []Observation {
	result := make([]Observation, 0, len(mongoRows)+len(redisRows))
	heads := map[string]bool{}
	for _, r := range mongoRows {
		o := classify(r)
		result = append(result, o)
		if o.RawHead != nil {
			heads[o.CID] = true
		}
	}
	for _, r := range redisRows {
		o := Observation{Source: "redis:" + digest([]byte(r.Key)), Reasons: []string{"redis-key-does-not-prove-typed-origin"}}
		if strings.HasPrefix(r.Key, "MAX_SEQ:") {
			o.CID = strings.TrimPrefix(r.Key, "MAX_SEQ:")
			n, e := strconv.ParseInt(r.RawHead, 10, 64)
			if r.Type != "string" || e != nil || n < 0 || n == math.MaxInt64 {
				o.Reasons = append(o.Reasons, "invalid-raw-redis-head")
			} else {
				o.RawHead = &n
			}
			if r.PTTL >= 0 {
				o.Reasons = append(o.Reasons, "expiring-raw-head")
			}
		}
		result = append(result, o)
		if o.RawHead != nil {
			heads[o.CID] = true
		}
	}
	for i := range result {
		if result[i].CID != "" && !heads[result[i].CID] {
			result[i].Reasons = append(result[i].Reasons, "no-observed-raw-head-zero-not-inferred")
		}
	}
	return result
}
func fixedReasons(i Identity) []string {
	r := []string{"external-drain-unverified", "old-binary-exclusion-unverified", "historical-lifecycle-coverage-unverified", "two-pass-observation-is-not-cross-store-atomicity"}
	if !i.TenantAttested {
		r = append(r, "tenant-is-operator-binding-not-storage-attestation")
	}
	return r
}
func snapshotDigest(s Snapshot) string { s.SHA = ""; b, _ := json.Marshal(s); return digest(b) }
func (s Snapshot) Public() PublicSummary {
	p := PublicSummary{State: State, SnapshotSHA: snapshotDigest(s), MongoRows: len(s.Mongo), RedisKeys: len(s.Redis), Reasons: map[string]int{}}
	for _, r := range fixedReasons(s.Identity) {
		p.Reasons[r]++
	}
	candidates := map[string]map[string]bool{}
	for _, o := range observations(s.Mongo, s.Redis) {
		p.ErasedSlots += o.Erased
		for _, r := range o.Reasons {
			p.Reasons[r]++
		}
		if o.CID != "" {
			if candidates[o.CID] == nil {
				candidates[o.CID] = map[string]bool{}
			}
			for _, origin := range o.Origins {
				candidates[o.CID][origin.Kind+":"+origin.SHA] = true
			}
		}
	}
	for _, origins := range candidates {
		if len(origins) > 1 {
			p.CandidateCollisions++
			p.Reasons["ambiguous-current-typed-origin-candidates"]++
		}
	}
	return p // No CID/account/tenant/DB/endpoint/credentials/payload in public JSON.
}
