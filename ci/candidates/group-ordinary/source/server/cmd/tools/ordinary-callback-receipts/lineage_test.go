package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type callbackLineageBackend struct {
	*callbackBackendFixture
	want messagedelivery.KafkaOfflineCallbackSummary
	t    *testing.T
}

func (b *callbackLineageBackend) Confirm(ctx context.Context, actual messagedelivery.KafkaOfflineCallbackSummary, proof messagedelivery.KafkaOfflineCallbackConfirmation) error {
	if actual != b.want || actual.PreviousSnapshotSHA256 == "" {
		b.t.Fatal("confirmation lost the original inspected lineage")
	}
	return b.callbackBackendFixture.Confirm(ctx, actual, proof)
}
func TestCallbackOperatorNonemptyLineageInspectJSONIsConfirmableAndStaleReceiptStillRejects(t *testing.T) {
	args, backend, dir := callbackOperatorFixture(t)
	backend.summary.PreviousSnapshotSHA256 = strings.Repeat("9", 64)
	inspected := execute(context.Background(), args, callbackFactory(backend))
	if inspected.code != 0 || backend.confirms != 0 {
		t.Fatal("inspect was not a read-only success")
	}
	raw, err := json.Marshal(inspected.value)
	if err != nil || !bytes.Contains(raw, []byte(`"PreviousSnapshotSHA256":"`+backend.summary.PreviousSnapshotSHA256+`"`)) {
		t.Fatal("actual inspect JSON lost nonempty lineage", err)
	}
	raw = append(raw, '\n')
	path := filepath.Join(dir, "inspect-receipt.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	confirmArgs, _ := callbackConfirmationFixture(t, args, backend, dir)
	for i, arg := range confirmArgs {
		if arg == "--expected-receipt" {
			confirmArgs[i+1] = path
		}
		if arg == "--expected-receipt-sha" {
			confirmArgs[i+1] = operatorHash(raw)
		}
	}
	exact := &callbackLineageBackend{callbackBackendFixture: backend, want: backend.summary, t: t}
	factory := func(context.Context, operatorInput) (callbackBackend, error) { return exact, nil }
	confirmed := execute(context.Background(), confirmArgs, factory)
	if confirmed.code != 0 || callbackState(confirmed) != "confirmation_committed_no_callback_or_vendor_call" || backend.confirms != 1 {
		t.Fatal("readPrivateDoc/decodeDoc rejected nonempty inspected lineage")
	}
	backend.summary.Revision++
	backend.summary.PreviousSnapshotSHA256 = strings.Repeat("8", 64)
	backend.summary.RecordSHA256 = strings.Repeat("6", 64)
	for i, arg := range confirmArgs {
		if arg == "--journal" {
			confirmArgs[i+1] = filepath.Join(dir, "stale-confirm.jsonl")
		}
	}
	stale := execute(context.Background(), confirmArgs, factory)
	if stale.code != 2 || callbackState(stale) != "expected_receipt_changed" || backend.confirms != 1 || backend.inspects != 3 || backend.closes != 3 {
		t.Fatal("old lineage receipt confirmed a newer record or leaked lifecycle")
	}
}
