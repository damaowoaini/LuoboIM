package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

// `ordinary-callback-receipts group-owned` is an explicit successor mode.
// It uses dedicated private credentials/purpose and has no send/replay method.
const ownedGroupPurpose = "luobo-owned-group-notification-operator/v1"

type ownedGroupEvidence struct {
	Version                 string                        `json:"version"`
	Kind                    string                        `json:"kind"`
	Decision                string                        `json:"decision"`
	ExpectedReceiptSHA256   string                        `json:"expectedReceiptSHA256"`
	IndependentSourceSHA256 string                        `json:"independentSourceSHA256"`
	Report                  *messagedelivery.OnlineReport `json:"report"`
	RetainedProfile         *deliveryprofile.Group        `json:"retainedProfile"`
}
type ownedGroupDrain struct {
	Version                 string                                 `json:"version"`
	InstanceID              string                                 `json:"instanceID"`
	BusinessAfterInstanceID string                                 `json:"businessAfterInstanceID"`
	WorkerJoined            bool                                   `json:"workerJoined"`
	OwnedPoolsClosed        bool                                   `json:"ownedPoolsClosed"`
	Policies                []string                               `json:"policies"`
	Profiles                []string                               `json:"profiles"`
	Legacy                  *messagedelivery.OwnedLegacyDrainProof `json:"legacy"`
}
type ownedGroupConfirmation struct {
	evidence              ownedGroupEvidence
	drain                 ownedGroupDrain
	evidenceSHA, drainSHA string
}
type ownedGroupBackend interface {
	Inspect(context.Context, string, string, string) (any, error)
	Confirm(context.Context, string, any, ownedGroupConfirmation) error
	Close(context.Context) error
}
type ownedGroupBackendFactory func(context.Context, operatorInput) (ownedGroupBackend, error)
type mongoOwnedGroupBackend struct{ original *mongoCallbackBackend }

func openOwnedGroupBackend(ctx context.Context, input operatorInput) (ownedGroupBackend, error) {
	backend, err := openMongoBackend(ctx, input)
	if err != nil {
		return nil, err
	}
	concrete, ok := backend.(*mongoCallbackBackend)
	if !ok {
		_ = backend.Close(ctx)
		return nil, errInput
	}
	return &mongoOwnedGroupBackend{original: concrete}, nil
}
func (b *mongoOwnedGroupBackend) Close(ctx context.Context) error { return b.original.Close(ctx) }
func (b *mongoOwnedGroupBackend) Inspect(ctx context.Context, kind, id, stage string) (any, error) {
	switch kind {
	case "message":
		return b.original.store.InspectOwnedMessageCallback(ctx, id, stage)
	case "silent":
		return b.original.store.InspectOwnedSilent(ctx, id)
	case "legacy":
		return b.original.store.InspectLegacyGroupNotification(ctx, id)
	case "business-after":
		return b.original.store.InspectOwnedBusinessAfter(ctx, id)
	}
	return nil, errInput
}
func (b *mongoOwnedGroupBackend) Confirm(ctx context.Context, kind string, expected any, c ownedGroupConfirmation) error {
	drain := messagedelivery.KafkaAttemptDrainProof{InstanceID: c.drain.InstanceID, EvidenceSHA256: c.drainSHA}
	switch kind {
	case "message":
		value, ok := expected.(messagedelivery.OwnedMessageSummary)
		if !ok {
			return errInput
		}
		return b.original.store.ConfirmOwnedMessageCallback(ctx, value, c.evidence.Decision, c.evidenceSHA, drain)
	case "silent":
		value, ok := expected.(messagedelivery.OwnedSilentSummary)
		if !ok {
			return errInput
		}
		if c.evidence.Decision == "applied" && c.evidence.Report != nil {
			return b.original.store.ConfirmOwnedSilentApplied(ctx, value, *c.evidence.Report, c.evidenceSHA, drain)
		}
		if c.evidence.Decision == "not-applied" {
			return b.original.store.ConfirmOwnedSilentNotApplied(ctx, value, c.evidenceSHA, drain)
		}
	case "business-after":
		value, ok := expected.(groupeffects.OwnedRoleCallbackRef)
		if !ok {
			return errInput
		}
		drain.InstanceID = c.drain.BusinessAfterInstanceID
		return b.original.store.ConfirmOwnedBusinessAfter(ctx, value, c.evidence.Decision, c.evidenceSHA, drain)
	case "legacy":
		value, ok := expected.(groupeffects.LegacyNotificationSummary)
		if !ok || c.drain.Legacy == nil {
			return errInput
		}
		return b.original.store.ConfirmLegacyGroupNotification(ctx, value, c.evidence.Decision, c.evidence.RetainedProfile, c.evidenceSHA, *c.drain.Legacy)
	}
	return errInput
}

// Exact recursively canonical keys and duplicate/case ambiguity rejection. No
// numeric roundtrip through float64: revisions beyond2^53 remain exact.
func ownedGroupDecode(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var scan func(int) error
	scan = func(depth int) error {
		if depth > 32 {
			return errInput
		}
		token, err := decoder.Token()
		if err != nil {
			return errInput
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				folded := strings.ToLower(name)
				if err != nil || !ok || seen[folded] {
					return errInput
				}
				seen[folded] = true
				if scan(depth+1) != nil {
					return errInput
				}
			}
			token, err = decoder.Token()
			if err != nil || token != json.Delim('}') {
				return errInput
			}
		case '[':
			for decoder.More() {
				if scan(depth+1) != nil {
					return errInput
				}
			}
			token, err = decoder.Token()
			if err != nil || token != json.Delim(']') {
				return errInput
			}
		default:
			return errInput
		}
		return nil
	}
	if scan(0) != nil || decoder.Decode(new(any)) != io.EOF {
		return errInput
	}
	typed := json.NewDecoder(bytes.NewReader(raw))
	typed.DisallowUnknownFields()
	if typed.Decode(destination) != nil {
		return errInput
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return errInput
	}
	var original, reencoded any
	first, second := json.NewDecoder(bytes.NewReader(raw)), json.NewDecoder(bytes.NewReader(canonical))
	first.UseNumber()
	second.UseNumber()
	if first.Decode(&original) != nil || second.Decode(&reencoded) != nil {
		return errInput
	}
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(reencoded)
	if !bytes.Equal(a, b) {
		return errInput
	}
	return nil
}
func ownedGroupExpected(kind string, raw []byte) (any, error) {
	switch kind {
	case "message":
		var value messagedelivery.OwnedMessageSummary
		if ownedGroupDecode(raw, &value) != nil {
			return nil, errInput
		}
		return value, nil
	case "silent":
		var value messagedelivery.OwnedSilentSummary
		if ownedGroupDecode(raw, &value) != nil {
			return nil, errInput
		}
		return value, nil
	case "business-after":
		var value groupeffects.OwnedRoleCallbackRef
		if ownedGroupDecode(raw, &value) != nil {
			return nil, errInput
		}
		return value, nil
	case "legacy":
		var value groupeffects.LegacyNotificationSummary
		if ownedGroupDecode(raw, &value) != nil {
			return nil, errInput
		}
		return value, nil
	}
	return nil, errInput
}
func ownedGroupContains(values []string, wanted string) bool {
	found := false
	previous := ""
	for _, v := range values {
		if !operatorSHA(v) || v <= previous {
			return false
		}
		previous = v
		found = found || v == wanted
	}
	return found
}
func ownedGroupProof(kind string, expected any, e ownedGroupEvidence, d ownedGroupDrain, expectedSHA, evidenceSHA, drainSHA string) (ownedGroupConfirmation, error) {
	c := ownedGroupConfirmation{evidence: e, drain: d, evidenceSHA: evidenceSHA, drainSHA: drainSHA}
	if e.Version != "owned-group-recovery-evidence-v1" || e.Kind != kind || e.ExpectedReceiptSHA256 != expectedSHA || !operatorSHA(e.IndependentSourceSHA256) || !operatorSHA(expectedSHA) || !operatorSHA(evidenceSHA) || !operatorSHA(drainSHA) {
		return c, errInput
	}
	if kind == "legacy" {
		value, ok := expected.(groupeffects.LegacyNotificationSummary)
		if !ok || d.Version != "owned-group-legacy-drained-v1" || d.Legacy == nil || d.Legacy.Epoch != value.Epoch || !d.Legacy.LegacyProducersStopped || !d.Legacy.ReceiverWorkersJoined || !operatorSHA(d.Legacy.EvidenceSHA256) || e.Report != nil {
			return c, errInput
		}
		if e.Decision == "redact-incomplete" && e.RetainedProfile == nil {
			return c, nil
		}
		if e.Decision == "restore-original-cut" && e.RetainedProfile != nil && value.OriginalCutSHA256 != "" {
			_, err := e.RetainedProfile.Fingerprint()
			return c, err
		}
		return c, errInput
	}
	if d.Version != "owned-group-action-drained-v1" || !d.WorkerJoined || !d.OwnedPoolsClosed || d.Legacy != nil || e.RetainedProfile != nil {
		return c, errInput
	}
	switch value := expected.(type) {
	case messagedelivery.OwnedMessageSummary:
		if kind != "message" || d.InstanceID != value.InstanceID || !ownedGroupContains(d.Profiles, value.ProfileSHA256) || e.Report != nil || e.Decision != "applied" && e.Decision != "not-applied" && e.Decision != "rejected" {
			return c, errInput
		}
	case groupeffects.OwnedRoleCallbackRef:
		identity := value.TransportSHA256
		if identity == "" {
			identity = value.ConfigurationSHA256
		}
		if kind != "business-after" || d.BusinessAfterInstanceID != value.InstanceID || !ownedGroupContains(d.Profiles, identity) || e.Report != nil || e.Decision != "applied" && e.Decision != "not-applied" {
			return c, errInput
		}
	case messagedelivery.OwnedSilentSummary:
		if kind != "silent" || d.InstanceID != value.InstanceID || !ownedGroupContains(d.Policies, value.PolicySHA256) || e.Decision != "applied" && e.Decision != "not-applied" || e.Decision == "applied" && e.Report == nil || e.Decision == "not-applied" && e.Report != nil {
			return c, errInput
		}
	default:
		return c, errInput
	}
	return c, nil
}
func executeOwnedGroup(parent context.Context, args []string, open ownedGroupBackendFactory) (outcome commandResult) {
	flags := flag.NewFlagSet("ordinary-callback-receipts group-owned", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "dedicated private operator input")
	inputSHA := flags.String("input-sha", "", "pin")
	database := flags.String("expected-database", "", "explicit DB binding")
	kind := flags.String("kind", "", "message/silent/legacy/business-after")
	id := flags.String("id", "", "original effect/branch UUID")
	stage := flags.String("stage", "", "message stage only")
	decision := flags.String("confirm", "", "empty is read-only inspect")
	expectedPath := flags.String("expected-receipt", "", "exact original summary")
	expectedSHA := flags.String("expected-receipt-sha", "", "pin")
	evidencePath := flags.String("evidence", "", "independent original result")
	evidenceSHA := flags.String("evidence-sha", "", "pin")
	drainPath := flags.String("drain", "", "exact lifecycle/producer drain")
	drainSHA := flags.String("drain-sha", "", "pin")
	journalPath := flags.String("journal", "", "fresh private journal")
	if parent == nil || open == nil || flags.Parse(args) != nil || flags.NArg() != 0 || !canonicalUUID(*id) || *kind != "message" && *kind != "silent" && *kind != "legacy" && *kind != "business-after" || *kind == "message" && *stage != "before-message" && *stage != "after-message" || *kind != "message" && *stage != "" {
		return result("invalid_owned_group_input", 2)
	}
	if parent.Err() != nil {
		return result("cancelled_before_input", 4)
	}
	raw, err := readPrivateDoc(*input, *inputSHA)
	var document operatorInput
	if err != nil || ownedGroupDecode(raw, &document) != nil || document.Purpose != ownedGroupPurpose {
		return result("invalid_owned_group_operator_input", 2)
	}
	for i := range raw {
		raw[i] = 0
	}
	document.Purpose = purpose
	converted, _ := json.Marshal(document)
	connection, err := decodeInput(converted, *database)
	for i := range converted {
		converted[i] = 0
	}
	if err != nil {
		return result("invalid_owned_group_connection_binding", 2)
	}
	var expected any
	var proof ownedGroupConfirmation
	var journal *privateJournal
	committed := false
	if *decision != "" {
		raw, err = readPrivateDoc(*expectedPath, *expectedSHA)
		if err != nil {
			return result("invalid_owned_expected_receipt", 2)
		}
		expected, err = ownedGroupExpected(*kind, raw)
		if err != nil {
			return result("invalid_owned_expected_receipt", 2)
		}
		raw, err = readPrivateDoc(*evidencePath, *evidenceSHA)
		var e ownedGroupEvidence
		if err != nil || ownedGroupDecode(raw, &e) != nil || e.Decision != *decision {
			return result("invalid_owned_evidence", 2)
		}
		raw, err = readPrivateDoc(*drainPath, *drainSHA)
		var d ownedGroupDrain
		if err != nil || ownedGroupDecode(raw, &d) != nil {
			return result("invalid_owned_drain", 2)
		}
		proof, err = ownedGroupProof(*kind, expected, e, d, *expectedSHA, *evidenceSHA, *drainSHA)
		if err != nil {
			return result("owned_confirmation_contract_rejected", 2)
		}
		journal, err = newPrivateJournal(*journalPath)
		if err != nil {
			return result("fresh_private_journal_required", 2)
		}
		defer func() {
			if journal.Close() != nil {
				if committed {
					outcome = result("owned_confirmation_committed_journal_close_unknown", 4)
				} else {
					outcome = result("owned_journal_close_unknown_inspect_same_receipt", 4)
				}
			}
		}()
		if journal.Write(map[string]string{"state": "prepared_not_dispatched", "expectedReceiptSHA256": *expectedSHA, "evidenceSHA256": *evidenceSHA, "drainSHA256": *drainSHA}) != nil {
			return result("owned_journal_unconfirmed", 4)
		}
	} else if *expectedPath != "" || *expectedSHA != "" || *evidencePath != "" || *evidenceSHA != "" || *drainPath != "" || *drainSHA != "" || *journalPath != "" {
		return result("owned_inspect_rejects_mutation_inputs", 2)
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	backend, err := open(ctx, connection)
	if err != nil || backend == nil {
		return result("owned_catalog_or_connection_unconfirmed", 4)
	}
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if backend.Close(end) != nil {
			if committed {
				outcome = result("owned_confirmation_committed_disconnect_unknown", 4)
			} else {
				outcome = result("owned_disconnect_unknown", 4)
			}
		}
	}()
	actual, err := backend.Inspect(ctx, *kind, *id, *stage)
	if err != nil {
		return result("owned_original_receipt_unconfirmed", 4)
	}
	if *decision == "" {
		return commandResult{actual, 0}
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return result("owned_expected_receipt_changed", 2)
	}
	if journal.Write(map[string]string{"state": "confirmation_attempt_starting", "expectedReceiptSHA256": *expectedSHA}) != nil {
		return result("owned_journal_unconfirmed", 4)
	}
	if err := backend.Confirm(ctx, *kind, expected, proof); err != nil {
		_ = journal.Write(map[string]string{"state": "unconfirmed_inspect_same_receipt_no_replay"})
		return result("owned_confirmation_unconfirmed_inspect_same_receipt_no_replay", 4)
	}
	committed = true
	if journal.Write(map[string]string{"state": "confirmation_committed_no_external_send"}) != nil {
		return result("owned_confirmation_committed_journal_unknown", 4)
	}
	return result("owned_confirmation_committed_no_external_send", 0)
}
