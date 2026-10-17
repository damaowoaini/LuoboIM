package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

var _ namespaceimport.OriginalHistorySegmentPager = (*OriginalHistoryVerifier)(nil)

// Raw ranges are candidate membership history, not a per-message grant.
// The only body contract is minted below from actual source evidence or
// inherited through the complete original verified-page/publication chain.
func originalScopeRefs(groups ...[]namespaceimport.SourceRef) []namespaceimport.SourceRef {
	values := map[string]namespaceimport.SourceRef{}
	for _, rows := range groups {
		for _, r := range rows {
			values[directHistoryDigest(r)] = r
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]namespaceimport.SourceRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, values[k])
	}
	return out
}
func originalScopedEvidence(raw bson.Raw, ref namespaceimport.SourceRef) error {
	if raw.Validate() != nil || !importUniqueFields(raw) || !validSHA(ref.ObjectSHA256) || !validSHA(ref.RecordSHA256) || !identity(ref.Kind) {
		return ErrConflict
	}
	return nil
}

func (v *OriginalHistoryVerifier) originalGroupSequencePermission(ctx context.Context, h namespaceimport.Header, row namespaceimport.OwnerHistorySegment, seq int64, reader namespaceimport.OriginalHistoryRecordReader) (bool, []namespaceimport.SourceRef, error) {
	slots, ok := reader.(namespaceimport.OriginalHistorySequenceReader)
	if !ok {
		return false, nil, ErrUnprepared
	}
	body, ref, e := slots.ReadOriginalHistorySourceAtSequence(ctx, row.CID, seq)
	if e != nil {
		return false, nil, e
	}
	raw := bson.Raw(body)
	if originalScopedEvidence(raw, ref) != nil {
		return false, nil, ErrConflict
	}
	epoch, eok := raw.Lookup("epoch").StringValueOK()
	source, sok := raw.Lookup("source_id").StringValueOK()
	branch, bok := raw.Lookup("branch_id").StringValueOK()
	if !eok || !sok || !bok || !validUUID(source) || !validUUID(branch) {
		return false, nil, ErrConflict
	}
	trust, e := v.checkTrust(h, epoch)
	if e != nil {
		return false, []namespaceimport.SourceRef{ref}, e
	}
	c := &originalHistoryChain{ctx: ctx, reader: reader, trust: trust, decoder: &Store{epoch: epoch, activationSHA: trust.ActivationSHA256}, refs: map[string]namespaceimport.SourceRef{directHistoryDigest(ref): ref}}
	actual, e := c.read(Effects, key(epoch, "branch", branch))
	if e != nil {
		return false, c.evidence(), e
	}
	if string(actual) != string(raw) {
		return false, c.evidence(), ErrConflict
	}
	d, e := c.origin(row.CID)
	if e != nil {
		return false, c.evidence(), e
	}
	if d.Kind != "group" || d.SHA256 != row.OriginSHA256 {
		return false, c.evidence(), ErrConflict
	}
	var duty deliveryDuty
	if bson.Unmarshal(raw, &duty) != nil || duty.ID != key(epoch, "branch", branch) || duty.Epoch != epoch || duty.SourceID != source || duty.BranchID != branch || duty.CID != row.CID || duty.Seq != seq || !duty.Persist || duty.NamespaceOriginKind != d.Kind || duty.NamespaceOriginSHA256 != d.SHA256 || !validSHA(duty.InputSHA) || !validSHA(duty.PayloadSHA) {
		return false, c.evidence(), ErrConflict
	}
	receiptRaw, e := c.read(Receipts, key(epoch, "source", source))
	if e != nil {
		return false, c.evidence(), e
	}
	receipt, e := c.decoder.decodeNoticeSourceReceipt(receiptRaw)
	if e != nil || receipt.SourceID != source || receipt.InputSHA256 != duty.InputSHA {
		return false, c.evidence(), ErrConflict
	}
	found := false
	for _, b := range receipt.Branches {
		if b.ID == branch {
			if found || b.ConversationID != d.CID || b.Seq != seq || !b.Persist || b.Hidden != duty.Hidden {
				return false, c.evidence(), ErrConflict
			}
			found = true
		}
	}
	if !found {
		return false, c.evidence(), ErrConflict
	}
	if duty.Hidden {
		return false, c.evidence(), nil
	}
	if e = c.receipt(d, source, branch, duty.InputSHA, directHistoryDigest(receipt), 1, seq); e != nil {
		return false, c.evidence(), e
	}
	policyRaw, e := c.read(Receipts, key(epoch, "kafka-policy", duty.PolicySHA))
	if e != nil {
		return false, c.evidence(), e
	}
	var policy kafkaPolicyRow
	if bson.Unmarshal(policyRaw, &policy) != nil || policy.ID != key(epoch, "kafka-policy", duty.PolicySHA) || policy.Kind != "kafka-policy-v1" || policy.Epoch != epoch || policy.SHA256 != duty.PolicySHA || policy.Policy.Epoch != epoch || policy.Policy.ActivationSHA256 != trust.ActivationSHA256 {
		return false, c.evidence(), ErrConflict
	}
	policySHA, e := policy.Policy.Fingerprint()
	if e != nil || policySHA != duty.PolicySHA || policy.Policy.Version != duty.ProducerVersion {
		return false, c.evidence(), ErrConflict
	}
	if duty.ProducerVersion != "ordinary-kafka-v1" && duty.ProducerVersion != ownedGroupProducer {
		return false, c.evidence(), ErrUnprepared
	}
	if duty.ProducerVersion == ownedGroupProducer {
		if duty.ActionProfile == nil || duty.ActionCut == nil || !duty.ActionCut.Valid() || duty.ActionProducer != "group-role-v1" && duty.ActionProducer != "group-action-v1" {
			return false, c.evidence(), ErrConflict
		}
		sum, e := duty.ActionProfile.Fingerprint()
		if e != nil || sum != duty.ActionProfileSHA || KafkaDeliveryPolicy(duty.ActionProfile.Transport) != policy.Policy {
			return false, c.evidence(), ErrConflict
		}
	}

	if duty.ProducerVersion == "ordinary-kafka-v1" {
		original, e := c.read(Receipts, key(epoch, "kafka-source", source))
		if e != nil {
			return false, c.evidence(), e
		}
		var binding kafkaReceipt
		if bson.Unmarshal(original, &binding) != nil || binding.ID != key(epoch, "kafka-source", source) || binding.Kind != "kafka-source-v1" || binding.Epoch != epoch || binding.SourceID != source || binding.InputSHA256 != receipt.InputSHA256 || binding.ReceiptSHA256 != directHistoryDigest([]any{receipt, duty.PolicySHA}) || binding.PolicySHA256 != duty.PolicySHA || !validSHA(binding.RecordSHA256) {
			return false, c.evidence(), ErrConflict
		}
	}
	// Modern immutable scopes remain valid even after duty payload/targets were
	// privacy-scrubbed. They bind the ORIGINAL epoch/branch/receipt/payload.
	scopeID, iok := raw.Lookup("history_scope_id").StringValueOK()
	scopeSHA, hok := raw.Lookup("history_scope_sha256").StringValueOK()
	if iok || hok {
		if !iok || !hok || scopeID != groupScopeID(epoch, branch) || !validSHA(scopeSHA) {
			return false, c.evidence(), ErrConflict
		}
		original, e := c.read(Receipts, scopeID)
		if e != nil {
			return false, c.evidence(), e
		}
		var record groupHistoryScopeRecord
		var scope groupHistoryScope
		if bson.Unmarshal(original, &record) != nil || record.ID != scopeID || record.Kind != "group-history-scope-v1" || record.SHA != scopeSHA || hash([]byte(record.JSON)) != scopeSHA || decodeImportJSON(record.JSON, &scope) != nil || !scope.valid(d, seq) || scope.Epoch != epoch || scope.SourceID != source || scope.BranchID != branch || scope.InputSHA != receipt.InputSHA256 || scope.ReceiptSHA != directHistoryDigest(receipt) || scope.PayloadSHA != duty.PayloadSHA || directHistoryDigest(scope) != scopeSHA || duty.ProducerVersion == ownedGroupProducer && scope.Kind != "group-history-targets-v1" {
			return false, c.evidence(), ErrConflict
		}
		return scope.allows(row.OwnerID), c.evidence(), nil
	}
	allowed, e := c.legacyAcceptedGroupPermission(d, row.OwnerID, receipt, duty, slots)
	return allowed, c.evidence(), e
}

// This interprets only the two reviewed actual producer contracts. A retained
// ordinary payload proves broadcast type. Targeted notifications additionally
// require the COMPLETE original frozen envelope/InputSHA, not current roster,
// recipient_count, a target hash, an erased empty slice or a later signature.
func (c *originalHistoryChain) legacyAcceptedGroupPermission(d NamespaceOriginDescriptor, owner string, receipt Receipt, duty deliveryDuty, slots namespaceimport.OriginalHistorySequenceReader) (bool, error) {
	message, e := c.originalAcceptedMessage(d, duty, slots)
	if e != nil {
		return false, e
	}
	seenDenied := map[string]bool{}
	for _, denied := range duty.VisibilityDenied {
		if !validSHA(denied) || seenDenied[denied] {
			return false, ErrConflict
		}
		seenDenied[denied] = true
		if denied == hash([]byte(owner)) {
			return false, nil
		}
	}
	if duty.ProducerVersion == "ordinary-kafka-v1" && message.ContentType < constant.NotificationBegin && !msgprocessor.IsNotificationByMsg(message) {
		return true, nil
	}
	envelope := Envelope{ID: receipt.SourceID}
	for _, accepted := range receipt.Branches {
		raw, e := c.read(Effects, key(c.trust.Epoch, "branch", accepted.ID))
		if e != nil {
			return false, e
		}
		var branch deliveryDuty
		if bson.Unmarshal(raw, &branch) != nil || branch.ID != key(c.trust.Epoch, "branch", accepted.ID) || branch.Epoch != c.trust.Epoch || branch.SourceID != receipt.SourceID || branch.BranchID != accepted.ID || branch.CID != accepted.ConversationID || branch.Seq != accepted.Seq || branch.Persist != accepted.Persist || branch.Hidden != accepted.Hidden || branch.InputSHA != receipt.InputSHA256 {
			return false, ErrConflict
		}
		// Erased recipients cannot be reconstructed from count/current membership.
		recipients := raw.Lookup("recipients")
		if recipients.Type == 0 || int64(len(branch.Recipients)) != branch.RecipientCount {
			return false, ErrUnprepared
		}
		if accepted.Hidden {
			return false, ErrUnprepared
		}
		var original *sdkws.MsgData
		if accepted.Persist {
			desc, e := c.origin(branch.CID)
			if e != nil {
				return false, e
			}
			original, e = c.originalAcceptedMessage(desc, branch, slots)
			if e != nil {
				return false, e
			}
		} else {
			if len(branch.Payload) == 0 || hash(branch.Payload) != branch.PayloadSHA {
				return false, ErrUnprepared
			}
			original = &sdkws.MsgData{}
			if proto.Unmarshal(branch.Payload, original) != nil {
				return false, ErrConflict
			}
		}
		copyMessage := proto.Clone(original).(*sdkws.MsgData)
		copyMessage.Seq = 0
		body, e := proto.MarshalOptions{Deterministic: true}.Marshal(copyMessage)
		if e != nil {
			return false, e
		}
		envelope.Branches = append(envelope.Branches, Branch{ID: accepted.ID, ConversationID: accepted.ConversationID, Payload: body, Recipients: append([]string(nil), branch.Recipients...), Persist: accepted.Persist})
	}
	frozen, sum, e := freezeEnvelopeMode(envelope, duty.ProducerVersion == ownedGroupProducer)
	if e != nil || sum != receipt.InputSHA256 {
		return false, ErrConflict
	}
	for _, branch := range frozen.Branches {
		if branch.ID == duty.BranchID {
			for _, uid := range branch.Recipients {
				if uid == owner {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, ErrConflict
}

func (c *originalHistoryChain) originalAcceptedMessage(d NamespaceOriginDescriptor, duty deliveryDuty, slots namespaceimport.OriginalHistorySequenceReader) (*sdkws.MsgData, error) {
	message := &sdkws.MsgData{}
	if len(duty.Payload) > 0 {
		if hash(duty.Payload) != duty.PayloadSHA || proto.Unmarshal(duty.Payload, message) != nil {
			return nil, ErrConflict
		}
	} else {
		model := table.MsgDocModel{}
		id := model.GetDocID(d.CID, duty.Seq)
		body, ref, e := slots.ReadOriginalHistoryMessageDocument(c.ctx, id)
		if e != nil {
			return nil, e
		}
		raw := bson.Raw(body)
		if originalScopedEvidence(raw, ref) != nil {
			return nil, ErrConflict
		}
		c.refs[directHistoryDigest(ref)] = ref
		var doc table.MsgDocModel
		if bson.Unmarshal(raw, &doc) != nil || doc.DocID != id {
			return nil, ErrConflict
		}
		index := model.GetMsgIndex(duty.Seq)
		if index < 0 || int(index) >= len(doc.Msg) || doc.Msg[index] == nil || doc.Msg[index].Msg == nil {
			return nil, ErrUnprepared
		}
		message = convert.MsgDB2Pb(doc.Msg[index].Msg)
		actual, e := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if e != nil || hash(actual) != duty.PayloadSHA {
			return nil, ErrUnprepared
		}
	}
	actual, e := DescribeNamespace(message)
	if e != nil || actual.CID != d.CID || actual.SHA256 != d.SHA256 || message.Seq != duty.Seq || message.SendID != duty.Sender {
		return nil, ErrConflict
	}
	return message, nil
}

type originalScopeCursor struct {
	Index     int    `json:"index"`
	Sequence  int64  `json:"sequence"`
	Metadata  bool   `json:"metadata"`
	SourceSHA string `json:"source_sha256"`
}

func originalCandidateSHA(rows []namespaceimport.OwnerHistorySegment) string {
	return directHistoryDigest(rows)
}

// At most ONE original sequence per invocation, independently resumable.
// There is no total range/owner/history cap, and no range hull over a hole.
func (v *OriginalHistoryVerifier) DeriveOriginalHistorySegmentPage(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, epoch, kind, id string, reader namespaceimport.OriginalHistoryRecordReader, after string) (namespaceimport.OriginalHistorySegmentPage, error) {
	out := namespaceimport.OriginalHistorySegmentPage{}
	candidates, e := v.deriveOriginalHistoryCandidateRanges(ctx, h, i, epoch, kind, id, reader)
	if e != nil {
		return out, e
	}
	cursor := originalScopeCursor{SourceSHA: originalCandidateSHA(candidates)}
	if after != "" {
		if namespaceinventory.DecodeStrict([]byte(after), &cursor) != nil || cursor.SourceSHA != originalCandidateSHA(candidates) || cursor.Index < 0 || cursor.Index > len(candidates) {
			return out, ErrConflict
		}
	}
	if cursor.Index == len(candidates) {
		out.Complete = true
		return out, nil
	}
	spent := 0
	for cursor.Index < len(candidates) && spent < 1 {
		row := candidates[cursor.Index]
		// DescribeNamespace via original proof is already part of candidates. A
		// direct namespace does not acquire group semantics from a CID prefix.
		trust, e := v.checkTrust(h, row.SourceEpoch)
		if e != nil {
			return out, e
		}
		chain := &originalHistoryChain{ctx: ctx, reader: reader, trust: trust, decoder: &Store{epoch: trust.Epoch, activationSHA: trust.ActivationSHA256}, refs: map[string]namespaceimport.SourceRef{}}
		origin, e := chain.origin(row.CID)
		if e != nil {
			return out, e
		}
		if origin.Kind != "group" || row.ScopeContract == namespaceimport.OriginalGroupSequenceScopeV1 {
			out.Segments = append(out.Segments, row)
			cursor.Index++
			cursor.Sequence = 0
			cursor.Metadata = false
			spent++
			continue
		}
		// Old signed namespace metadata is retained for the no-body/query branch;
		// it is NOT a body grant. Independently derive scoped source leaves next.
		metadata := row.SourceKind == "namespace-import-boundary-v1" || row.SourceKind == "namespace-owner-imported-history-v1"
		if metadata && !cursor.Metadata {
			out.Segments = append(out.Segments, row)
			cursor.Metadata = true
			spent++
			continue
		}
		if cursor.Sequence == 0 {
			cursor.Sequence = row.Lower
		}
		if cursor.Sequence < row.Lower || cursor.Sequence > row.Upper {
			return out, ErrConflict
		}
		seq := cursor.Sequence
		allowed, refs, e := v.originalGroupSequencePermission(ctx, h, row, seq, reader)
		spent++
		if e != nil {
			if !errors.Is(e, ErrUnprepared) && !errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
				return out, e
			}
			out.Pending = append(out.Pending, namespaceimport.OriginalHistorySequencePending{Sequence: seq, Reason: "original-slot-scope-unavailable", Evidence: originalScopeRefs(row.Evidence, refs)})
		} else if allowed {
			point := row
			point.ScopeContract = namespaceimport.OriginalGroupSequenceScopeV1
			point.Lower = seq
			point.Upper = seq
			point.Evidence = originalScopeRefs(row.Evidence, refs)
			n := len(out.Segments)
			if n > 0 {
				last := &out.Segments[n-1]
				if last.ScopeContract == point.ScopeContract && last.SourceEpoch == point.SourceEpoch && last.SourceKind == point.SourceKind && last.SourceID == point.SourceID && last.CID == point.CID && last.OwnerID == point.OwnerID && last.Upper+1 == point.Lower {
					last.Upper = point.Upper
					last.Evidence = originalScopeRefs(last.Evidence, point.Evidence)
				} else {
					out.Segments = append(out.Segments, point)
				}
			} else {
				out.Segments = append(out.Segments, point)
			}
		}
		if seq == row.Upper {
			cursor.Index++
			cursor.Sequence = 0
			cursor.Metadata = false
		} else {
			cursor.Sequence++
		}
	}
	out.Complete = cursor.Index == len(candidates)
	if !out.Complete {
		body, e := json.Marshal(cursor)
		if e != nil {
			return out, e
		}
		out.Next = string(body)
	}
	return out, nil
}

func (v *OriginalHistoryVerifier) DeriveOriginalHistorySegments(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, epoch, kind, id string, reader namespaceimport.OriginalHistoryRecordReader) ([]namespaceimport.OwnerHistorySegment, error) {
	out := []namespaceimport.OwnerHistorySegment{}
	cursor := ""
	for {
		page, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, epoch, kind, id, reader, cursor)
		if e != nil {
			return nil, e
		}
		if len(page.Pending) > 0 {
			return nil, &namespaceimport.OriginalHistoryProofPending{Sequence: page.Pending[0].Sequence, Reason: page.Pending[0].Reason, Evidence: page.Pending[0].Evidence}
		}
		for _, segment := range page.Segments {
			n := len(out)
			if n > 0 {
				last := &out[n-1]
				if last.ScopeContract == segment.ScopeContract && last.SourceEpoch == segment.SourceEpoch && last.SourceKind == segment.SourceKind && last.SourceID == segment.SourceID && last.CID == segment.CID && last.OwnerID == segment.OwnerID && last.Upper+1 == segment.Lower && segment.Upper-last.Lower < 8 {
					last.Upper = segment.Upper
					last.Evidence = originalScopeRefs(last.Evidence, segment.Evidence)
					continue
				}
			}
			out = append(out, segment)
		}
		if page.Complete {
			return out, nil
		}
		if page.Next == "" || page.Next == cursor {
			return nil, ErrConflict
		}
		cursor = page.Next
	}
}

func (v *OriginalHistoryVerifier) VerifyOriginalHistorySegment(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, w namespaceimport.OwnerHistorySegment, reader namespaceimport.OriginalHistoryRecordReader) error {
	if w.ValidateShape() != nil || w.SourceEpoch == h.Epoch {
		return ErrInvalid
	}
	candidates, e := v.deriveOriginalHistoryCandidateRanges(ctx, h, i, w.SourceEpoch, w.SourceKind, w.SourceID, reader)
	if e != nil {
		return e
	}
	for _, row := range candidates {
		if row.CID != w.CID || row.OwnerID != w.OwnerID || row.OriginSHA256 != w.OriginSHA256 || row.SourceEpoch != w.SourceEpoch || row.SourceKind != w.SourceKind || row.SourceID != w.SourceID || w.Lower < row.Lower || w.Upper > row.Upper {
			continue
		}
		trust, e := v.checkTrust(h, row.SourceEpoch)
		if e != nil {
			return e
		}
		chain := &originalHistoryChain{ctx: ctx, reader: reader, trust: trust, decoder: &Store{epoch: trust.Epoch, activationSHA: trust.ActivationSHA256}, refs: map[string]namespaceimport.SourceRef{}}
		origin, e := chain.origin(row.CID)
		if e != nil {
			return e
		}
		if origin.Kind != "group" || row.ScopeContract == namespaceimport.OriginalGroupSequenceScopeV1 {
			if directHistoryDigest(row) == directHistoryDigest(w) {
				return nil
			}
			continue
		}
		metadata := row.SourceKind == "namespace-import-boundary-v1" || row.SourceKind == "namespace-owner-imported-history-v1"
		if metadata && w.ScopeContract == "" {
			if directHistoryDigest(row) == directHistoryDigest(w) {
				return nil
			}
			continue
		}
		if w.ScopeContract != namespaceimport.OriginalGroupSequenceScopeV1 {
			return ErrConflict
		}
		if w.Upper-w.Lower >= 8 {
			return ErrConflict
		}
		refs := row.Evidence
		for seq := w.Lower; seq <= w.Upper; seq++ {
			allowed, source, e := v.originalGroupSequencePermission(ctx, h, row, seq, reader)
			if e != nil {
				return e
			}
			if !allowed {
				return ErrConflict
			}
			refs = originalScopeRefs(refs, source)
		}
		expected := row
		expected.ScopeContract = namespaceimport.OriginalGroupSequenceScopeV1
		expected.Lower = w.Lower
		expected.Upper = w.Upper
		expected.Evidence = refs
		if directHistoryDigest(expected) == directHistoryDigest(w) {
			return nil
		}
	}
	return ErrConflict
}
