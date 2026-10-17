package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
)

const unitID = "0cae74c8-ee1c-48c1-aa72-32e7b1d04f25"

type fakeReceiptStore struct {
	reads, writes int
	err           error
}

func (f *fakeReceiptStore) Inspect(context.Context, string) (groupeffects.Summary, error) {
	f.reads++
	return groupeffects.Summary{ID: unitID, Kind: groupeffects.AfterCallback, State: groupeffects.Unconfirmed, Revision: 3}, f.err
}
func (f *fakeReceiptStore) Confirm(context.Context, groupeffects.Confirmation) (groupeffects.Summary, error) {
	f.writes++
	return groupeffects.Summary{ID: unitID, Kind: groupeffects.AfterCallback, State: groupeffects.Done, Revision: 4}, f.err
}
func operatorFixture(t *testing.T) ([]string, string) {
	t.Helper()
	raw := t.TempDir()
	dir, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	input := operatorInput{URI: "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration", Database: "luobo_effect_operator_unit", ReplicaSet: "luoboIntegration"}
	body, _ := json.Marshal(input)
	file := filepath.Join(dir, "operator.json")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("127.0.0.1:37017"))
	return []string{"--operator-input", file, "--expected-database", input.Database, "--expected-replica-set", input.ReplicaSet, "--expected-endpoints-sha256", hex.EncodeToString(sum[:]), "--id", unitID}, dir
}
func confirmArguments(args []string, dir string) []string {
	return append(append([]string(nil), args...), "--mode", "confirm", "--journal", filepath.Join(dir, "confirmation.jsonl"), "--expected-sender-instance", unitID, "--expected-revision", "3", "--expected-payload-sha256", strings.Repeat("a", 64), "--decision", "applied", "--remote-outcome-evidence-sha256", strings.Repeat("b", 64), "--worker-drain-evidence-sha256", strings.Repeat("c", 64))
}
func TestOperatorDefaultsToInspectAndNeverDispatchesOrConfirms(t *testing.T) {
	args, _ := operatorFixture(t)
	f := &fakeReceiptStore{}
	closed := false
	var out, stderr bytes.Buffer
	code := execute(context.Background(), args, &out, &stderr, func(ctx context.Context, _ operatorInput) (connection, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded connection")
		}
		return connection{store: f, close: func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded disconnect")
			}
			closed = true
			return nil
		}}, nil
	})
	if code != 0 || f.reads != 1 || f.writes != 0 || !closed {
		t.Fatal("default operator path performed mutation or omitted cleanup")
	}
}
func TestOperatorRejectsMissingBindingOrConfirmationEvidenceBeforeConnection(t *testing.T) {
	base, dir := operatorFixture(t)
	for _, args := range [][]string{nil, append(append([]string(nil), base...), "--mode", "retry"), append(append([]string(nil), base...), "--mode", "confirm"), append(confirmArguments(base, dir), "--worker-drain-evidence-sha256", "")} {
		calls := 0
		var out, stderr bytes.Buffer
		code := execute(context.Background(), args, &out, &stderr, func(context.Context, operatorInput) (connection, error) { calls++; return connection{}, nil })
		if code != 2 || calls != 0 {
			t.Fatal("inexact operator request reached a connector")
		}
	}
}
func TestOperatorInputRejectsWrongDatabaseEndpointPermissionAndSymlink(t *testing.T) {
	args, dir := operatorFixture(t)
	a, err := parse(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadInput(a.input, "other", a.replica, a.endpoints); err == nil {
		t.Fatal("wrong database accepted")
	}
	if _, err := loadInput(a.input, a.database, a.replica, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong endpoint accepted")
	}
	if err := os.Chmod(a.input, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadInput(a.input, a.database, a.replica, a.endpoints); err == nil {
		t.Fatal("public operator input accepted")
	}
	if err := os.Chmod(a.input, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.json")
	if err := os.Symlink(a.input, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadInput(link, a.database, a.replica, a.endpoints); err == nil {
		t.Fatal("symlink operator input accepted")
	}
}
func TestOperatorPreservesUnknownConfirmationAndDoesNotPrintRawErrors(t *testing.T) {
	base, dir := operatorFixture(t)
	f := &fakeReceiptStore{err: groupeffects.ErrUnconfirmed}
	var out, stderr bytes.Buffer
	code := execute(context.Background(), confirmArguments(base, dir), &out, &stderr, func(context.Context, operatorInput) (connection, error) {
		return connection{store: f, close: func(context.Context) error { return errors.New("synthetic-private-detail") }}, nil
	})
	if code != 3 || f.writes != 1 || f.reads != 0 || strings.Contains(stderr.String(), "synthetic-private-detail") {
		t.Fatal("unknown ACK was retried, downgraded or leaked")
	}
	body, err := os.ReadFile(filepath.Join(dir, "confirmation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("confirmation_intent")) || !bytes.Contains(body, []byte("operator_result_unconfirmed")) {
		t.Fatal("unknown confirmation lacks durable intention/result")
	}
}
func TestOperatorJournalNeverOverwritesPriorOrPartialResult(t *testing.T) {
	base, dir := operatorFixture(t)
	target := filepath.Join(dir, "confirmation.jsonl")
	before := []byte("prior partial evidence\n")
	if err := os.WriteFile(target, before, 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	var out, stderr bytes.Buffer
	code := execute(context.Background(), confirmArguments(base, dir), &out, &stderr, func(context.Context, operatorInput) (connection, error) { called = true; return connection{}, nil })
	after, err := os.ReadFile(target)
	if err != nil || code != 2 || called || !bytes.Equal(before, after) {
		t.Fatal("operator resumed/overwrote an earlier journal")
	}
}
func TestOperatorConnectionFailureIsFixedAndStillClosesPartialConnection(t *testing.T) {
	args, _ := operatorFixture(t)
	closed := false
	var out, stderr bytes.Buffer
	code := execute(context.Background(), args, &out, &stderr, func(context.Context, operatorInput) (connection, error) {
		return connection{close: func(context.Context) error { closed = true; return nil }}, errors.New("synthetic-private-URI")
	})
	if code != 1 || !closed || strings.Contains(stderr.String(), "synthetic-private-URI") {
		t.Fatal("connection failure leaked data or lost cleanup")
	}
}
