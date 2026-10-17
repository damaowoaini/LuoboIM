package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ownedOperatorFixture struct {
	summary          messagedelivery.OwnedMessageSummary
	confirms, closes int
	unknown          bool
}

func (b *ownedOperatorFixture) Inspect(context.Context, string, string, string) (any, error) {
	return b.summary, nil
}
func (b *ownedOperatorFixture) Confirm(_ context.Context, _ string, expected any, _ ownedGroupConfirmation) error {
	if expected != b.summary {
		return errInput
	}
	b.confirms++
	if b.unknown {
		return errors.New("synthetic unknown ACK")
	}
	return nil
}
func (b *ownedOperatorFixture) Close(context.Context) error { b.closes++; return nil }
func ownedOperatorArgs(t *testing.T) ([]string, *ownedOperatorFixture, string) {
	t.Helper()
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture unavailable")
	}
	in := operatorInput{Version: 1, Purpose: ownedGroupPurpose, URI: "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration", Database: "synthetic_owned_operator", ReplicaSet: "luoboIntegration", Epoch: "41b33802-7959-4296-82e1-8d817bd8bf34", Activation: strings.Repeat("a", 64)}
	path, pin := callbackDoc(t, dir, "owned-input.json", in)
	b := &ownedOperatorFixture{summary: messagedelivery.OwnedMessageSummary{EffectID: "a42b00a9-d9f4-4d02-a80f-81e604180bf5", Stage: "before-message", ProfileSHA256: strings.Repeat("b", 64), RequestSHA256: strings.Repeat("c", 64), BodySHA256: strings.Repeat("d", 64), State: "unconfirmed", Revision: 9007199254740993, AttemptID: "cadf797c-28c0-45a9-907c-f01353dc8f4c", InstanceID: "bcd54f59-707d-49fb-a2dc-7c38b6ef7ab0", OutcomeSHA256: strings.Repeat("e", 64)}}
	return []string{"--input", path, "--input-sha", pin, "--expected-database", in.Database, "--kind", "message", "--id", b.summary.EffectID, "--stage", b.summary.Stage}, b, dir
}
func ownedConfirmArgs(t *testing.T, args []string, b *ownedOperatorFixture, dir string) []string {
	t.Helper()
	path, pin := callbackDoc(t, dir, "expected.json", b.summary)
	e := ownedGroupEvidence{Version: "owned-group-recovery-evidence-v1", Kind: "message", Decision: "not-applied", ExpectedReceiptSHA256: pin, IndependentSourceSHA256: strings.Repeat("f", 64)}
	ePath, eSHA := callbackDoc(t, dir, "evidence.json", e)
	d := ownedGroupDrain{Version: "owned-group-action-drained-v1", InstanceID: b.summary.InstanceID, BusinessAfterInstanceID: "2d6fd398-7b2e-40e4-b9b0-0be2642d6538", WorkerJoined: true, OwnedPoolsClosed: true, Policies: []string{}, Profiles: []string{b.summary.ProfileSHA256}}
	dPath, dSHA := callbackDoc(t, dir, "drain.json", d)
	return append(append([]string(nil), args...), "--confirm", "not-applied", "--expected-receipt", path, "--expected-receipt-sha", pin, "--evidence", ePath, "--evidence-sha", eSHA, "--drain", dPath, "--drain-sha", dSHA, "--journal", filepath.Join(dir, "journal.jsonl"))
}
func TestOwnedGroupOperatorInspectsAndConfirmsExactHighRevisionAndRejectsStale(t *testing.T) {
	args, b, dir := ownedOperatorArgs(t)
	factory := func(context.Context, operatorInput) (ownedGroupBackend, error) { return b, nil }
	out := executeOwnedGroup(context.Background(), args, factory)
	if out.code != 0 || b.confirms != 0 {
		t.Fatal("default inspect mutated or failed")
	}
	raw, _ := json.Marshal(out.value)
	var decoded messagedelivery.OwnedMessageSummary
	if ownedGroupDecode(raw, &decoded) != nil || decoded.Revision != b.summary.Revision {
		t.Fatal("actual inspect JSON lost revision precision")
	}
	confirm := ownedConfirmArgs(t, args, b, dir)
	out = executeOwnedGroup(context.Background(), confirm, factory)
	if out.code != 0 || b.confirms != 1 {
		t.Fatal("private expected receipt did not reach exact confirmation")
	}
	b.summary.Revision++
	for i, a := range confirm {
		if a == "--journal" {
			confirm[i+1] = filepath.Join(dir, "stale.jsonl")
		}
	}
	out = executeOwnedGroup(context.Background(), confirm, factory)
	if out.code != 2 || callbackState(out) != "owned_expected_receipt_changed" || b.confirms != 1 || b.closes != 3 {
		t.Fatal("stale receipt borrowed a later state or leaked backend")
	}
}
func TestOwnedGroupOperatorUnknownDoesNotReplayAndRejectsNestedAmbiguity(t *testing.T) {
	args, b, dir := ownedOperatorArgs(t)
	b.unknown = true
	confirm := ownedConfirmArgs(t, args, b, dir)
	factory := func(context.Context, operatorInput) (ownedGroupBackend, error) { return b, nil }
	out := executeOwnedGroup(context.Background(), confirm, factory)
	if out.code != 4 || b.confirms != 1 || callbackState(out) != "owned_confirmation_unconfirmed_inspect_same_receipt_no_replay" {
		t.Fatal("unknown was classified as applied or retried")
	}
	out = executeOwnedGroup(context.Background(), confirm, factory)
	if out.code != 2 || b.confirms != 1 {
		t.Fatal("existing journal replayed unknown mutation")
	}
	for _, raw := range []string{`{"x":{"a":1,"a":2}}`, `{"x":{"state":1,"State":1}}`} {
		var value any
		if ownedGroupDecode([]byte(raw), &value) == nil {
			t.Fatal("nested ambiguous evidence accepted")
		}
	}
}
