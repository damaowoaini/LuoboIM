package group

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/tools/errs"
)

func TestGroupIntentSealsActorAndOrderedTargets(t *testing.T) {
	keys := []string{"g", "a", "b"}
	i, err := captureGroupIntent(memberEditContext("actor"), keys...)
	if err != nil {
		t.Fatal(err)
	}
	keys[1] = "changed"
	if err := i.check(memberEditContext("actor"), "g", "a", "b"); err != nil {
		t.Fatal("capture borrowed caller slice", err)
	}
	for _, tc := range []struct {
		actor string
		keys  []string
	}{{"other", []string{"g", "a", "b"}}, {"actor", []string{"h", "a", "b"}}, {"actor", []string{"g", "b", "a"}}, {"actor", []string{"g", "a"}}} {
		if err := i.check(memberEditContext(tc.actor), tc.keys...); !errs.ErrNoPermission.Is(err) {
			t.Fatal("retargeted identity accepted", err)
		}
	}
	if _, err := captureGroupIntent(context.Background(), "g"); !errs.ErrNoPermission.Is(err) {
		t.Fatal("empty actor accepted")
	}
}
