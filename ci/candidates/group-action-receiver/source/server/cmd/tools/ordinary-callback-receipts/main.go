package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
)

type evidence struct {
	Version                 string `json:"version"`
	Decision                string `json:"decision"`
	BranchID                string `json:"branchID"`
	PolicySHA256            string `json:"policySHA256"`
	Binding                 string `json:"binding"`
	InputSHA256             string `json:"inputSHA256"`
	BodySHA256              string `json:"bodySHA256"`
	AttemptID               string `json:"attemptID"`
	ResponseSHA256          string `json:"responseSHA256"`
	Response                []byte `json:"responseBase64"`
	IndependentSourceSHA256 string `json:"independentSourceSHA256"`
}
type drainReceipt struct {
	Version          string   `json:"version"`
	InstanceID       string   `json:"instanceID"`
	WorkerJoined     bool     `json:"workerJoined"`
	OwnedPoolsClosed bool     `json:"ownedPoolsClosed"`
	Policies         []string `json:"policies"`
}
type commandResult struct {
	value any
	code  int
}

func result(state string, code int) commandResult {
	return commandResult{map[string]string{"state": state}, code}
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout)
	cancel()
	os.Exit(code)
}
func run(ctx context.Context, args []string, writer io.Writer) int {
	var out commandResult
	if len(args) > 0 && args[0] == "group-owned" {
		out = executeOwnedGroup(ctx, args[1:], openOwnedGroupBackend)
	} else {
		out = execute(ctx, args, openMongoBackend)
	}
	if json.NewEncoder(writer).Encode(out.value) != nil {
		return 4
	}
	return out.code
}
func confirmation(expected messagedelivery.KafkaOfflineCallbackSummary, e evidence, drain drainReceipt, decision, evidenceSHA, drainSHA string) (messagedelivery.KafkaOfflineCallbackConfirmation, error) {
	if e.Version != "ordinary-offline-callback-evidence-v1" || e.Decision != decision || e.BranchID != expected.BranchID || e.PolicySHA256 != expected.PolicySHA256 || e.Binding != expected.Binding || e.InputSHA256 != expected.InputSHA256 || e.BodySHA256 != expected.BodySHA256 || e.AttemptID != expected.AttemptID || !operatorSHA(e.IndependentSourceSHA256) || !operatorSHA(evidenceSHA) || !operatorSHA(drainSHA) || drain.Version != "ordinary-offline-drained-v1" || drain.InstanceID != expected.InstanceID || !drain.WorkerJoined || !drain.OwnedPoolsClosed {
		return messagedelivery.KafkaOfflineCallbackConfirmation{}, errInput
	}
	found := false
	previous := ""
	for _, p := range drain.Policies {
		if !operatorSHA(p) || p <= previous {
			return messagedelivery.KafkaOfflineCallbackConfirmation{}, errInput
		}
		previous = p
		if p == expected.PolicySHA256 {
			found = true
		}
	}
	if !found {
		return messagedelivery.KafkaOfflineCallbackConfirmation{}, errInput
	}
	proof := messagedelivery.KafkaOfflineCallbackConfirmation{Decision: decision, EvidenceSHA256: evidenceSHA, Drain: messagedelivery.KafkaAttemptDrainProof{InstanceID: drain.InstanceID, EvidenceSHA256: drainSHA}}
	switch decision {
	case "applied":
		if !operatorSHA(e.ResponseSHA256) || operatorHash(e.Response) != e.ResponseSHA256 {
			return proof, errInput
		}
		users, info, err := push.ParseKafkaOfflineCallbackEvidence(e.Response)
		if err != nil {
			return proof, errInput
		}
		proof.Users = users
		proof.Info = info
		proof.ResponseSHA256 = e.ResponseSHA256
	case "not-applied", "rejected":
		if e.ResponseSHA256 != "" || len(e.Response) > 0 {
			return proof, errInput
		}
	default:
		return proof, errInput
	}
	return proof, nil
}
func execute(parent context.Context, args []string, open callbackBackendFactory) (outcome commandResult) {
	flags := flag.NewFlagSet("ordinary-callback-receipts", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "dedicated private operator file")
	inputSHA := flags.String("input-sha", "", "exact pin")
	database := flags.String("expected-database", "", "explicit database")
	branch := flags.String("branch", "", "original UUID")
	decision := flags.String("confirm", "", "empty is read-only inspect")
	expectedPath := flags.String("expected-receipt", "", "original minimal summary")
	expectedSHA := flags.String("expected-receipt-sha", "", "pin")
	evidencePath := flags.String("evidence", "", "independent evidence")
	evidenceSHA := flags.String("evidence-sha", "", "pin")
	drainPath := flags.String("drain", "", "original joined worker/pools receipt")
	drainSHA := flags.String("drain-sha", "", "pin")
	journalPath := flags.String("journal", "", "fresh no-clobber journal")
	if parent == nil || open == nil || flags.Parse(args) != nil || flags.NArg() != 0 || !canonicalUUID(*branch) {
		return result("invalid_input", 2)
	}
	if parent.Err() != nil {
		return result("cancelled_before_input", 4)
	}
	connection, err := loadInput(*input, *database, *inputSHA)
	if err != nil {
		return result("invalid_operator_input", 2)
	}
	var expected messagedelivery.KafkaOfflineCallbackSummary
	var proof messagedelivery.KafkaOfflineCallbackConfirmation
	var journal *privateJournal
	knownCommit := false
	if *decision != "" {
		raw, err := readPrivateDoc(*expectedPath, *expectedSHA)
		keys := map[string]bool{"BranchID": true, "PolicySHA256": true, "Binding": true, "InputSHA256": true, "BodySHA256": true, "OutputSHA256": true, "State": true, "AttemptID": true, "InstanceID": true, "OutcomeSHA256": true, "RecordSHA256": true, "Revision": true, "Erased": true, "PreviousSnapshotSHA256": true}
		if err != nil || decodeDoc(raw, keys, &expected) != nil || expected.BranchID != *branch {
			return result("invalid_expected_receipt", 2)
		}
		raw, err = readPrivateDocBounded(*evidencePath, *evidenceSHA, 64<<20)
		var e evidence
		if err != nil || decodeDoc(raw, map[string]bool{"version": true, "decision": true, "branchID": true, "policySHA256": true, "binding": true, "inputSHA256": true, "bodySHA256": true, "attemptID": true, "responseSHA256": true, "responseBase64": true, "independentSourceSHA256": true}, &e) != nil {
			return result("invalid_independent_evidence", 2)
		}
		raw, err = readPrivateDoc(*drainPath, *drainSHA)
		var drain drainReceipt
		if err != nil || decodeDoc(raw, map[string]bool{"version": true, "instanceID": true, "workerJoined": true, "ownedPoolsClosed": true, "policies": true}, &drain) != nil {
			return result("invalid_drain", 2)
		}
		proof, err = confirmation(expected, e, drain, *decision, *evidenceSHA, *drainSHA)
		if err != nil {
			return result("confirmation_contract_rejected", 2)
		}
		journal, err = newPrivateJournal(*journalPath)
		if err != nil {
			return result("fresh_journal_required", 2)
		}
		defer func() {
			if journal.Close() != nil {
				if knownCommit {
					outcome = result("committed_journal_close_unconfirmed", 4)
				} else {
					outcome = result("journal_close_unconfirmed_inspect_original", 4)
				}
			}
		}()
		if journal.Write(map[string]string{"state": "prepared_not_dispatched", "expectedReceiptSHA256": *expectedSHA, "evidenceSHA256": *evidenceSHA, "drainSHA256": *drainSHA}) != nil {
			return result("journal_unconfirmed", 4)
		}
	} else if *expectedPath != "" || *expectedSHA != "" || *evidencePath != "" || *evidenceSHA != "" || *drainPath != "" || *drainSHA != "" || *journalPath != "" {
		return result("inspect_rejects_mutation_input", 2)
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	backend, err := open(ctx, connection)
	if err != nil || backend == nil {
		return result("operator_connection_or_catalog_unconfirmed", 4)
	}
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if backend.Close(end) != nil {
			if knownCommit {
				outcome = result("committed_disconnect_unconfirmed", 4)
			} else {
				outcome = result("operator_disconnect_unconfirmed", 4)
			}
		}
	}()
	actual, err := backend.Inspect(ctx, *branch)
	if err != nil {
		return result("original_receipt_unconfirmed", 4)
	}
	if *decision == "" {
		return commandResult{actual, 0}
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return result("expected_receipt_changed", 2)
	}
	if journal.Write(map[string]string{"state": "confirmation_attempt_starting", "recordSHA256": actual.RecordSHA256, "decision": *decision}) != nil {
		return result("journal_unconfirmed", 4)
	}
	if err := backend.Confirm(ctx, expected, proof); err != nil {
		_ = journal.Write(map[string]string{"state": "unconfirmed_inspect_original_no_replay"})
		return result("unconfirmed_inspect_original_no_replay", 4)
	}
	knownCommit = true
	if journal.Write(map[string]string{"state": "confirmation_committed", "recordSHA256": actual.RecordSHA256}) != nil {
		return result("committed_journal_unconfirmed", 4)
	}
	return result("confirmation_committed_no_callback_or_vendor_call", 0)
}
