package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const directSourceOwnerKind = "direct-namespace-new-owner-v1"
const directHeldOwnerKind = "direct-namespace-held-owner-v1"
const directSegmentOwnerKind = "direct-namespace-segment-only-owner-v1"

// One shared Sequences row for direct and group prospective admissions.
// Admission is immutable; progress and true historical revocation are separate.
// Upper=0 means no accepted future source. Import Lower NEVER revokes base.
type namespaceOwnerProspective struct {
	ID                string `bson:"_id"`
	Kind              string `bson:"kind"`
	Epoch             string `bson:"epoch"`
	CID               string `bson:"conversation_id"`
	Owner             string `bson:"user_id"`
	OriginSHA         string `bson:"origin_sha256"`
	BaseSHA           string `bson:"base_sha256"`
	AdmissionJSON     string `bson:"admission_json"`
	AdmissionSHA      string `bson:"admission_sha256"`
	SourceID          string `bson:"source_id"`
	BranchID          string `bson:"branch_id"`
	ReceiptSHA        string `bson:"receipt_sha256"`
	InputSHA          string `bson:"input_sha256"`
	SeedMaximum       int64  `bson:"seed_maximum"`
	Lower             int64  `bson:"lower"`
	Upper             int64  `bson:"upper"`
	Revision          int64  `bson:"revision"`
	HistoricalFloor   int64  `bson:"historical_floor"`
	RevocationCommand string `bson:"revocation_command"`
	RevocationSHA     string `bson:"revocation_sha256"`
}
type namespaceOwnerAdmissionProof struct {
	ID               string `bson:"_id"`
	Kind             string `bson:"kind"`
	Epoch            string `bson:"epoch"`
	CID              string `bson:"conversation_id"`
	Owner            string `bson:"user_id"`
	OriginSHA        string `bson:"origin_sha256"`
	BaseSHA          string `bson:"base_sha256"`
	AdmissionJSON    string `bson:"admission_json"`
	AdmissionSHA     string `bson:"admission_sha256"`
	HeaderSHA        string `bson:"header_sha256"`
	InventorySHA     string `bson:"inventory_sha256"`
	PageID           string `bson:"page_id"`
	PageSHA          string `bson:"page_sha256"`
	SourceWindowJSON string `bson:"source_window_json"`
	SourceWindowSHA  string `bson:"source_window_sha256"`
}
type directHistoryPolicy struct {
	base                                            directNamespaceWindow
	seed, head, floor, futureUpper, historicalFloor int64
}

func directHistoryDigest(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return hash(b)
}
func namespaceOwnerProspectiveID(epoch, cid, owner string) string {
	return key(epoch, "namespace-owner-prospective", cid+"\x00"+owner)
}
func namespaceOwnerAdmissionID(epoch, cid, owner string) string {
	return key(epoch, "namespace-owner-admission", cid+"\x00"+owner)
}
func (p directHistoryPolicy) valid(d NamespaceOriginDescriptor, owner string) bool {
	b := p.base
	if d.Kind == "group" || validateNamespaceDescriptor(d) != nil || b.UserID != owner || b.ConversationID != d.CID || b.OriginSHA256 != d.SHA256 || b.Kind != "direct-namespace-window-v1" && b.Kind != directSourceOwnerKind && b.Kind != directHeldOwnerKind && b.Kind != directSegmentOwnerKind || b.Lower < 0 || b.Upper < 0 || !b.Closed && b.Upper != 0 || p.seed < 0 || p.head < p.seed || p.head >= math.MaxInt64 || p.floor < b.Lower || p.historicalFloor < 0 || p.futureUpper < 0 || p.futureUpper > p.head || p.futureUpper > 0 && p.futureUpper <= p.seed {
		return false
	}
	for _, uid := range d.OriginIDs {
		if uid == owner {
			return true
		}
	}
	return false
}
func (p directHistoryPolicy) segments() ownerHistorySegments {
	return ownerHistorySegments{BaseLower: p.base.Lower, BaseUpper: p.base.Upper, BaseClosed: p.base.Closed, SeedMaximum: p.seed, FutureLower: p.seed + 1, FutureUpper: p.futureUpper, Floor: p.floor, HistoricalFloor: p.historicalFloor}
}
func (p directHistoryPolicy) upper() int64          { return p.segments().upper() }
func (p directHistoryPolicy) allows(seq int64) bool { return p.segments().allows(seq) }
func (s *Store) directHistoryCounter(ctx context.Context, cid string) (counter, error) {
	if e := s.requireNamespaceCounter(ctx, cid); e != nil {
		return counter{}, e
	}
	var c counter
	e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", cid)}).Decode(&c)
	return c, e
}
func (s *Store) readDirectHistoryBase(ctx context.Context, d NamespaceOriginDescriptor, owner string) (directNamespaceWindow, string, error) {
	var raw bson.Raw
	var b directNamespaceWindow
	e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": directNamespaceWindowID(s.epoch, d.CID, owner)}).Decode(&raw)
	if e != nil {
		return b, "", e
	}
	if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "header_sha256", "boundary_sha256"}, []string{"lower", "upper"}, []string{"closed"}, "") || bson.Unmarshal(raw, &b) != nil || b.ID != directNamespaceWindowID(s.epoch, d.CID, owner) || b.Epoch != s.epoch || b.ConversationID != d.CID || b.UserID != owner || b.OriginSHA256 != d.SHA256 || b.Lower < 0 || b.Upper < 0 || b.Upper >= math.MaxInt64 || !b.Closed && b.Upper != 0 || !validSHA(b.HeaderSHA256) || !validSHA(b.BoundarySHA256) {
		return b, "", ErrConflict
	}
	sum := hash(raw)
	cut, cutSHA, e := s.namespaceCutover(ctx)
	if e != nil {
		return b, "", e
	}
	switch b.Kind {
	case "direct-namespace-window-v1":
		id := key(s.epoch, "namespace-import-boundary", cut.MigrationID+"\x00"+namespaceimport.EffectiveOwnerKey(d.CID, owner))
		var boundary namespaceImportBoundary
		var boundaryRaw bson.Raw
		if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&boundaryRaw); e != nil {
			if errors.Is(e, mongo.ErrNoDocuments) {
				return b, "", ErrUnprepared
			}
			return b, "", e
		}
		if !exactFields(boundaryRaw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "user_id", "owner_key_sha256", "boundary_sha256", "coverage_sha256", "boundary_json", "window_id", "window_sha256"}, nil, nil, "") || bson.Unmarshal(boundaryRaw, &boundary) != nil || boundary.ID != id || boundary.Kind != "namespace-import-boundary-v1" || boundary.ConversationID != d.CID || boundary.UserID != owner || boundary.OwnerKeySHA256 != namespaceimport.EffectiveOwnerKey(d.CID, owner) || boundary.WindowID != b.ID || boundary.WindowSHA256 != sum || boundary.HeaderSHA256 != b.HeaderSHA256 || boundary.BoundarySHA256 != b.BoundarySHA256 || !validSHA(boundary.InventorySHA256) || !validSHA(boundary.CoverageSHA256) {
			return b, "", ErrConflict
		}
		var original namespaceimport.EffectiveOwnerBoundary
		if decodeImportJSON(boundary.BoundaryJSON, &original) != nil {
			return b, "", ErrConflict
		}
		proof, e := namespaceimport.EffectiveBoundarySHA256(original)
		if e != nil || proof != b.BoundarySHA256 || original.Window.CID != b.ConversationID || original.Window.OwnerID != owner || original.Window.OriginSHA256 != d.SHA256 || original.Window.Lower != b.Lower || original.Window.Upper != b.Upper || original.Window.Closed != b.Closed {
			return b, "", ErrConflict
		}
		var completed namespaceImportCutover
		if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "namespace-import-cutover", cut.MigrationID)}).Decode(&completed); e != nil {
			if errors.Is(e, mongo.ErrNoDocuments) {
				return b, "", ErrUnprepared
			}
			return b, "", e
		}
		if completed.Kind != "namespace-import-cutover-v1" || completed.HeaderSHA256 != b.HeaderSHA256 || completed.ManifestSHA256 != cut.ManifestSHA256 || completed.CutoverSHA256 != cutSHA {
			return b, "", ErrConflict
		}
	case directHeldOwnerKind:
		if !b.Closed || b.Upper != 0 || b.Lower != 0 {
			return b, "", ErrConflict
		}
		proof, a, e := s.readOwnerAdmission(ctx, d, owner, sum)
		if e != nil {
			return b, "", e
		}
		if a.Kind != "held-cut-roster-v1" || proof.HeaderSHA != b.HeaderSHA256 || proof.AdmissionSHA != b.BoundarySHA256 {
			return b, "", ErrConflict
		}
	case directSourceOwnerKind:
		if !b.Closed || b.Upper != 0 || b.Lower != 0 || b.HeaderSHA256 != cutSHA {
			return b, "", ErrConflict
		}
		proof, admission, e := s.readOwnerAdmission(ctx, d, owner, sum)
		if e != nil {
			return b, "", e
		}
		if admission.Kind != "actual-positive-source-v1" || b.BoundarySHA256 != directHistoryDigest([]string{admission.SourceID, admission.BranchID, admission.ReceiptSHA256, owner}) || proof.BaseSHA != sum {
			return b, "", ErrConflict
		}
	default:
		return b, "", ErrUnprepared
	}
	return b, sum, nil
}
func (s *Store) readOwnerAdmission(ctx context.Context, d NamespaceOriginDescriptor, owner, baseSHA string) (namespaceOwnerAdmissionProof, namespaceimport.ProspectiveOwnerAdmission, error) {
	var raw bson.Raw
	var p namespaceOwnerAdmissionProof
	var a namespaceimport.ProspectiveOwnerAdmission
	e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": namespaceOwnerAdmissionID(s.epoch, d.CID, owner)}).Decode(&raw)
	if e != nil {
		if errors.Is(e, mongo.ErrNoDocuments) {
			e = ErrUnprepared
		}
		return p, a, e
	}
	if !ownerAdmissionProofShape(raw) || bson.Unmarshal(raw, &p) != nil || p.ID != namespaceOwnerAdmissionID(s.epoch, d.CID, owner) || p.Kind != "namespace-owner-admission-v1" || p.Epoch != s.epoch || p.CID != d.CID || p.Owner != owner || p.OriginSHA != d.SHA256 || p.BaseSHA != baseSHA || decodeImportJSON(p.AdmissionJSON, &a) != nil {
		return p, a, ErrConflict
	}
	sum, e := namespaceimport.ProspectiveAdmissionSHA256(a)
	if e != nil || sum != p.AdmissionSHA || a.CID != d.CID || a.OwnerID != owner || a.OriginSHA256 != d.SHA256 {
		return p, a, ErrConflict
	}
	switch a.Kind {
	case "actual-positive-source-v1":
		if p.HeaderSHA != "" || p.InventorySHA != "" || p.PageSHA != "" || p.PageID != "" {
			return p, a, ErrConflict
		}
		if d.Kind == "group" {
			var w MembershipWindow
			if decodeImportJSON(p.SourceWindowJSON, &w) != nil || hash([]byte(p.SourceWindowJSON)) != p.SourceWindowSHA || !s.validMembershipWindow(w, d.CID, owner) || w.GroupID != d.GroupID || w.Closed || w.Lower > a.Lower {
				return p, a, ErrConflict
			}
			var raw bson.Raw
			if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "new-group-seed", w.LastCommand)}).Decode(&raw); e != nil {
				return p, a, e
			}
			seed, e := s.decodeNewGroupSeed(raw)
			if e != nil || seed.CommandID != w.LastCommand || seed.GroupID != d.GroupID || seed.EvidenceSHA256 != w.SeedEvidenceSHA256 {
				return p, a, ErrConflict
			}
		} else if p.SourceWindowJSON != "" || p.SourceWindowSHA != "" {
			return p, a, ErrConflict
		}
		if e = s.checkProspectiveReceipt(ctx, d, a.SourceID, a.BranchID, a.InputSHA256, a.ReceiptSHA256, a.Lower, 0); e != nil {
			return p, a, e
		}
	case "held-cut-roster-v1":
		if p.SourceWindowJSON != "" || p.SourceWindowSHA != "" || p.HeaderSHA != a.HeaderSHA256 || !validSHA(p.InventorySHA) || !validSHA(p.PageSHA) || !identity(p.PageID) {
			return p, a, ErrConflict
		}
		h, inventory, _, e := s.readProspectivePageSource(ctx, p)
		if e != nil {
			return p, a, e
		}
		if a.InventoryBindingSHA256 != inventory.BindingSHA256() {
			return p, a, ErrConflict
		}
		// Reuse the strict original publication decoder, including canonical manifest
		// and known receipt bodies, rather than trusting three matching SHA fields.
		publisher := &NamespaceImportWriter{store: s, header: h}
		published, e := publisher.readImportCutover(ctx)
		if e != nil || !published.Found {
			if e == nil {
				e = ErrUnprepared
			}
			return p, a, e
		}
		cut, _, e := s.namespaceCutover(ctx)
		if e != nil {
			return p, a, e
		}
		if published.Receipt.ManifestSHA256 != cut.ManifestSHA256 {
			return p, a, ErrConflict
		}
	default:
		return p, a, ErrConflict
	}
	return p, a, nil
}
func (s *Store) checkProspectiveReceipt(ctx context.Context, d NamespaceOriginDescriptor, source, branchID, inputSHA, receiptSHA string, lower, upper int64) error {
	if !validUUID(source) || !validUUID(branchID) || !validSHA(inputSHA) || !validSHA(receiptSHA) || lower < 1 {
		return ErrConflict
	}
	var r Receipt
	if e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "source", source)}).Decode(&r); e != nil {
		return errors.Join(ErrUnprepared, e)
	}
	if r.ID != key(s.epoch, "source", source) || r.SourceID != source || r.Epoch != s.epoch || r.InputSHA256 != inputSHA || r.AcceptedAt.IsZero() || directHistoryDigest(r) != receiptSHA {
		return ErrConflict
	}
	actualSeq := int64(0)
	for _, b := range r.Branches {
		if b.ID == branchID {
			if actualSeq != 0 || b.ConversationID != d.CID || !b.Persist || b.Hidden || b.Seq < lower || upper != 0 && b.Seq != upper {
				return ErrConflict
			}
			actualSeq = b.Seq
		}
	}
	if actualSeq == 0 {
		return ErrConflict
	}
	var duty bson.Raw
	if e := s.db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(s.epoch, "branch", branchID)}).Decode(&duty); e != nil {
		return errors.Join(ErrUnprepared, e)
	}
	for k, want := range map[string]string{"epoch": s.epoch, "source_id": source, "branch_id": branchID, "conversation_id": d.CID, "namespace_origin_kind": d.Kind, "namespace_origin_sha256": d.SHA256, "input_sha256": inputSHA} {
		got, ok := duty.Lookup(k).StringValueOK()
		if !ok || got != want {
			return ErrConflict
		}
	}
	seq, ok := duty.Lookup("seq").Int64OK()
	persist, pok := duty.Lookup("persist").BooleanOK()
	if !ok || seq != actualSeq || !pok || !persist {
		return ErrConflict
	}
	return nil
}
func (s *Store) readOwnerProspective(ctx context.Context, d NamespaceOriginDescriptor, owner, baseSHA string) (namespaceOwnerProspective, error) {
	var raw bson.Raw
	var f namespaceOwnerProspective
	if e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceOwnerProspectiveID(s.epoch, d.CID, owner)}).Decode(&raw); e != nil {
		return f, e
	}
	if !ownerProspectiveShape(raw) || bson.Unmarshal(raw, &f) != nil || f.ID != namespaceOwnerProspectiveID(s.epoch, d.CID, owner) || f.Kind != "namespace-owner-prospective-v1" || f.Epoch != s.epoch || f.CID != d.CID || f.Owner != owner || f.OriginSHA != d.SHA256 || f.BaseSHA != baseSHA || f.SeedMaximum < 0 || f.SeedMaximum >= math.MaxInt64-1 || f.Lower != f.SeedMaximum+1 || f.Upper < 0 || f.Upper > 0 && f.Upper < f.Lower || f.Upper >= math.MaxInt64 || f.Revision < 1 || f.Revision >= math.MaxInt64-1 || f.HistoricalFloor < 0 || f.HistoricalFloor >= math.MaxInt64 {
		return f, ErrConflict
	}
	proof, a, e := s.readOwnerAdmission(ctx, d, owner, baseSHA)
	if e != nil {
		return f, e
	}
	if proof.AdmissionJSON != f.AdmissionJSON || proof.AdmissionSHA != f.AdmissionSHA || a.SeedMaximum != f.SeedMaximum || a.Lower != f.Lower {
		return f, ErrConflict
	}
	if f.Upper == 0 {
		if f.SourceID != "" || f.BranchID != "" || f.ReceiptSHA != "" || f.InputSHA != "" {
			return f, ErrConflict
		}
	} else if e = s.checkProspectiveReceipt(ctx, d, f.SourceID, f.BranchID, f.InputSHA, f.ReceiptSHA, f.Lower, f.Upper); e != nil {
		return f, e
	}
	if f.HistoricalFloor == 0 {
		if f.RevocationCommand != "" || f.RevocationSHA != "" {
			return f, ErrConflict
		}
	} else if e = s.checkOwnerHistoricalRevocation(ctx, d, owner, f); e != nil {
		return f, e
	}
	return f, nil
}
func (s *Store) directHistoryPolicy(ctx context.Context, d NamespaceOriginDescriptor, owner string) (directHistoryPolicy, error) {
	p := directHistoryPolicy{}
	participant := false
	for _, uid := range d.OriginIDs {
		participant = participant || uid == owner
	}
	if !participant {
		return p, ErrNamespaceHistoryNotOwned
	}
	head, e := s.directHistoryCounter(ctx, d.CID)
	if e != nil {
		return p, e
	}
	b, baseSHA, e := s.readDirectHistoryBase(ctx, d, owner)
	if errors.Is(e, mongo.ErrNoDocuments) {
		cut, cutSHA, cutErr := s.namespaceCutover(ctx)
		if cutErr != nil {
			return p, cutErr
		}
		extra, extraErr := s.additionalHistoryUpper(ctx, d, owner)
		if extraErr != nil {
			return p, extraErr
		}
		if extra == 0 {
			return p, ErrNamespaceHistoryNotOwned
		}
		// A virtual closed-empty base grants nothing. Each actual interval still
		// requires its own original verified page and publication proof below.
		b = directNamespaceWindow{ID: directNamespaceWindowID(s.epoch, d.CID, owner), Kind: directSegmentOwnerKind, Epoch: s.epoch, ConversationID: d.CID, UserID: owner, OriginSHA256: d.SHA256, Closed: true, HeaderSHA256: cutSHA, BoundarySHA256: directHistoryDigest([]string{cut.MigrationID, d.CID, owner})}
	} else if e != nil {
		return p, e
	}
	p = directHistoryPolicy{base: b, seed: head.SeedMaximum, head: head.Maximum, floor: b.Lower}
	future, e := s.readOwnerProspective(ctx, d, owner, baseSHA)
	if e == nil {
		if future.SeedMaximum != head.SeedMaximum || future.Upper > head.Maximum {
			return p, ErrConflict
		}
		p.futureUpper = future.Upper
		p.historicalFloor = future.HistoricalFloor
	} else if !errors.Is(e, mongo.ErrNoDocuments) {
		return p, e
	}
	driver := unrelation.NewMsgMongoDriver(s.db)
	floors, e := driver.ReadMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: d.CID}, {ConversationID: d.CID, UserID: owner}})
	if e != nil {
		return p, e
	}
	for _, f := range floors {
		p.floor = max(p.floor, f.MinSeq)
	}
	if !p.valid(d, owner) {
		return p, ErrConflict
	}
	return p, nil
}

// The legacy max_seq field cannot encode a closed-empty window. Managed
// consumers receive a finite source bound plus a separate metadata value;
// zero is never used here to authorize historical sequences.
func (r *HistoryWindowReader) ReadDirectNamespaceHistoryBounds(ctx context.Context, cid, owner string) (direct bool, lower, upper, conversationMaximum int64, resultErr error) {
	if ctx == nil {
		return false, 0, 0, 0, ErrInvalid
	}
	if r == nil {
		return false, 0, 0, 0, nil
	}
	if r.store == nil || !r.store.membershipRequired {
		return false, 0, 0, 0, ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s := r.store
	if e := s.CheckReady(current); e != nil {
		return false, 0, 0, 0, e
	}
	resultErr = s.transaction(current, func(tx context.Context) error {
		direct, lower, upper, conversationMaximum = false, 0, 0, 0
		if e := s.checkMembershipSchema(tx); e != nil {
			return e
		}
		d, e := s.readNamespacePrincipals(tx, cid)
		if e != nil {
			return e
		}
		if d.Kind == "group" {
			return nil
		}
		p, e := s.directHistoryPolicy(tx, d, owner)
		if e != nil {
			return e
		}
		extra, e := s.additionalHistoryUpper(tx, d, owner)
		if e != nil {
			return e
		}
		direct, lower, upper = true, max(p.floor, p.historicalFloor), max(p.upper(), extra)
		if p.base.Closed && p.futureUpper == 0 {
			conversationMaximum = max(p.base.Upper, extra)
		}
		return nil
	})
	return
}

func ownerAdmissionProofShape(raw bson.Raw) bool {
	return exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "base_sha256", "admission_json", "admission_sha256", "header_sha256", "inventory_sha256", "page_id", "page_sha256", "source_window_json", "source_window_sha256"}, nil, nil, "")
}
func ownerProspectiveShape(raw bson.Raw) bool {
	return exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "base_sha256", "admission_json", "admission_sha256", "source_id", "branch_id", "receipt_sha256", "input_sha256", "revocation_command", "revocation_sha256"}, []string{"seed_maximum", "lower", "upper", "revision", "historical_floor"}, nil, "")
}
