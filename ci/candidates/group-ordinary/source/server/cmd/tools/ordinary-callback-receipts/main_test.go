package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type callbackBackendFixture struct {
	summary                    messagedelivery.KafkaOfflineCallbackSummary
	inspects, confirms, closes int
	confirmErr, closeErr       error
	beforeConfirm              func()
}

func (b *callbackBackendFixture) Inspect(ctx context.Context, branch string) (messagedelivery.KafkaOfflineCallbackSummary, error) {
	b.inspects++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 20*time.Second || branch != b.summary.BranchID {
		return messagedelivery.KafkaOfflineCallbackSummary{}, errInput
	}
	return b.summary, nil
}
func (b *callbackBackendFixture) Confirm(context.Context, messagedelivery.KafkaOfflineCallbackSummary, messagedelivery.KafkaOfflineCallbackConfirmation) error {
	b.confirms++
	if b.beforeConfirm != nil {
		b.beforeConfirm()
	}
	return b.confirmErr
}
func (b *callbackBackendFixture) Close(ctx context.Context) error {
	b.closes++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 3*time.Second {
		return errInput
	}
	return b.closeErr
}
func callbackDoc(t *testing.T, dir, name string, value any) (string, string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, operatorHash(raw)
}
func callbackOperatorFixture(t *testing.T) ([]string, *callbackBackendFixture, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	input := operatorInput{Version: 1, Purpose: purpose, URI: "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration", Database: "synthetic_callback_operator", ReplicaSet: "luoboIntegration", Epoch: "41b33802-7959-4296-82e1-8d817bd8bf34", Activation: strings.Repeat("a", 64)}
	path, pin := callbackDoc(t, dir, "input.json", input)
	s := messagedelivery.KafkaOfflineCallbackSummary{BranchID: "a42b00a9-d9f4-4d02-a80f-81e604180bf5", PolicySHA256: strings.Repeat("b", 64), Binding: strings.Repeat("c", 64), InputSHA256: strings.Repeat("d", 64), BodySHA256: strings.Repeat("e", 64), State: "unconfirmed", AttemptID: "cadf797c-28c0-45a9-907c-f01353dc8f4c", InstanceID: "bcd54f59-707d-49fb-a2dc-7c38b6ef7ab0", OutcomeSHA256: strings.Repeat("f", 64), RecordSHA256: strings.Repeat("7", 64), Revision: 5}
	return []string{"--input", path, "--input-sha", pin, "--expected-database", input.Database, "--branch", s.BranchID}, &callbackBackendFixture{summary: s}, dir
}
func callbackConfirmationFixture(t *testing.T, args []string, b *callbackBackendFixture, dir string) ([]string, string) {
	t.Helper()
	expected, expectedSHA := callbackDoc(t, dir, "expected.json", b.summary)
	response := json.RawMessage(`{"actionCode":0,"userIDList":["member"],"offlinePushInfo":{"title":"synthetic"}}`)
	e := evidence{Version: "ordinary-offline-callback-evidence-v1", Decision: "applied", BranchID: b.summary.BranchID, PolicySHA256: b.summary.PolicySHA256, Binding: b.summary.Binding, InputSHA256: b.summary.InputSHA256, BodySHA256: b.summary.BodySHA256, AttemptID: b.summary.AttemptID, Response: response, ResponseSHA256: operatorHash(response), IndependentSourceSHA256: strings.Repeat("1", 64)}
	proof, proofSHA := callbackDoc(t, dir, "evidence.json", e)
	drain, drainSHA := callbackDoc(t, dir, "drain.json", drainReceipt{Version: "ordinary-offline-drained-v1", InstanceID: b.summary.InstanceID, WorkerJoined: true, OwnedPoolsClosed: true, Policies: []string{b.summary.PolicySHA256}})
	journal := filepath.Join(dir, "confirm.jsonl")
	return append(append([]string(nil), args...), "--confirm", "applied", "--expected-receipt", expected, "--expected-receipt-sha", expectedSHA, "--evidence", proof, "--evidence-sha", proofSHA, "--drain", drain, "--drain-sha", drainSHA, "--journal", journal), journal
}
func callbackFactory(b *callbackBackendFixture) callbackBackendFactory {
	return func(context.Context, operatorInput) (callbackBackend, error) { return b, nil }
}
func callbackState(out commandResult) string {
	m, _ := out.value.(map[string]string)
	return m["state"]
}
func TestCallbackOperatorDefaultInspectIsReadOnlyAndDisconnectIsChecked(t *testing.T) {
	args, b, _ := callbackOperatorFixture(t)
	r := execute(context.Background(), args, callbackFactory(b))
	if r.code != 0 || b.inspects != 1 || b.confirms != 0 || b.closes != 1 {
		t.Fatal("default inspect mutated or leaked lifecycle")
	}
	b.closeErr = errors.New("synthetic close failure")
	r = execute(context.Background(), args, callbackFactory(b))
	if r.code != 4 || callbackState(r) != "operator_disconnect_unconfirmed" {
		t.Fatal("unknown close called success")
	}
	raw, _ := json.Marshal(r.value)
	if bytes.Contains(raw, []byte("mongodb://")) || bytes.Contains(raw, []byte("synthetic_callback_operator")) {
		t.Fatal("operator output exposed private connection")
	}
}
func TestCallbackOperatorUnknownConfirmIsDurableOnceAndNeverAutoReplayed(t *testing.T) {
	args, b, dir := callbackOperatorFixture(t)
	args, journal := callbackConfirmationFixture(t, args, b, dir)
	b.confirmErr = messagedelivery.ErrKafkaPushCallbackUnconfirmed
	b.beforeConfirm = func() {
		raw, err := os.ReadFile(journal)
		if err != nil || !bytes.Contains(raw, []byte("confirmation_attempt_starting")) {
			t.Error("CAS preceded durable intent")
		}
	}
	r := execute(context.Background(), args, callbackFactory(b))
	if r.code != 4 || b.confirms != 1 || callbackState(r) != "unconfirmed_inspect_original_no_replay" {
		t.Fatal("unknown CAS became acknowledged")
	}
	r = execute(context.Background(), args, callbackFactory(b))
	if r.code != 2 || b.confirms != 1 || b.inspects != 1 {
		t.Fatal("existing journal triggered another CAS")
	}
}
func TestCallbackOperatorKnownCommitIsNotRolledBackByLocalCleanup(t *testing.T) {
	args, b, dir := callbackOperatorFixture(t)
	args, journal := callbackConfirmationFixture(t, args, b, dir)
	b.closeErr = errors.New("synthetic disconnect unknown")
	r := execute(context.Background(), args, callbackFactory(b))
	if r.code != 4 || callbackState(r) != "committed_disconnect_unconfirmed" || b.confirms != 1 {
		t.Fatal("known commit called rollback/clean")
	}
	raw, err := os.ReadFile(journal)
	if err != nil || !bytes.Contains(raw, []byte("confirmation_committed")) {
		t.Fatal("known commit not journaled", err)
	}
}
func TestCallbackOperatorStaleExpectedCannotBorrowNewerRecord(t *testing.T) {
	args, b, dir := callbackOperatorFixture(t)
	args, _ = callbackConfirmationFixture(t, args, b, dir)
	b.summary.Revision++
	r := execute(context.Background(), args, callbackFactory(b))
	if r.code != 2 || callbackState(r) != "expected_receipt_changed" || b.confirms != 0 {
		t.Fatal("stale summary confirmed a new record")
	}
}
func TestCallbackOperatorForeignResponseProofAndActiveInstanceReject(t *testing.T) {
	_, b, _ := callbackOperatorFixture(t)
	s := b.summary
	raw := json.RawMessage(`{"userIDList":["member"]}`)
	original := evidence{Version: "ordinary-offline-callback-evidence-v1", Decision: "applied", BranchID: s.BranchID, PolicySHA256: s.PolicySHA256, Binding: s.Binding, InputSHA256: s.InputSHA256, BodySHA256: s.BodySHA256, AttemptID: s.AttemptID, Response: raw, ResponseSHA256: operatorHash(raw), IndependentSourceSHA256: strings.Repeat("1", 64)}
	originalDrain := drainReceipt{Version: "ordinary-offline-drained-v1", InstanceID: s.InstanceID, WorkerJoined: true, OwnedPoolsClosed: true, Policies: []string{s.PolicySHA256}}
	if _, err := confirmation(s, original, originalDrain, "applied", strings.Repeat("2", 64), strings.Repeat("3", 64)); err != nil {
		t.Fatal("valid proof rejected", err)
	}
	for _, fault := range []string{"body", "input", "response", "attempt", "active", "pools", "policy", "foreign-instance"} {
		t.Run(fault, func(t *testing.T) {
			e, d := original, originalDrain
			switch fault {
			case "body":
				e.BodySHA256 = strings.Repeat("4", 64)
			case "input":
				e.InputSHA256 = strings.Repeat("4", 64)
			case "response":
				e.Response = []byte(`{"userIDList":["changed"]}`)
			case "attempt":
				e.AttemptID = "different"
			case "active":
				d.WorkerJoined = false
			case "pools":
				d.OwnedPoolsClosed = false
			case "policy":
				d.Policies = []string{strings.Repeat("4", 64)}
			case "foreign-instance":
				d.InstanceID = "different"
			}
			if _, err := confirmation(s, e, d, "applied", strings.Repeat("2", 64), strings.Repeat("3", 64)); err == nil {
				t.Fatal("foreign/active proof accepted")
			}
		})
	}
}
