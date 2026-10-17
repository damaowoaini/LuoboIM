//go:build linux

package ownedcontrol

import "testing"

func TestOwnedTargetMetadataParsingNeverFallsBackToDefaultDatabase(t *testing.T) {
	for _, c := range []struct {
		name, input string
		want        int
		valid       bool
	}{{"actual-private-db", "id=1 addr=127.0.0.1:1 db=15 cmd=client|info", 15, true}, {"zero-is-explicit", "id=1 db=0", 0, true}, {"missing", "id=1", 0, false}, {"duplicate", "db=15 db=15", 0, false}, {"foreign-range", "db=16", 0, false}, {"invalid", "db=unknown", 0, false}} {
		t.Run(c.name, func(t *testing.T) {
			got, e := clientDatabase(c.input)
			if (e == nil) != c.valid || c.valid && got != c.want {
				t.Fatalf("DB metadata accepted=%v wanted=%v", e == nil, c.valid)
			}
		})
	}
	if _, e := exactInfo("run_id:original\nrun_id:foreign\n", "run_id"); e == nil {
		t.Fatal("ambiguous target identity accepted")
	}
}
