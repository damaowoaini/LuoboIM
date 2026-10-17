package namespaceimport

import (
	"strings"
	"testing"
)

func TestEffectiveCoverageKeepsClosedEmptyDomain(t *testing.T) {
	ref := SourceRef{ObjectSHA256: strings.Repeat("a", 64), RecordSHA256: strings.Repeat("b", 64), Kind: "mongo:group_membership_history:original", Position: 1}
	base := EffectiveOwnerBoundary{Window: OwnerWindow{CID: "n_g", OriginSHA256: strings.Repeat("c", 64), GroupID: "g", OwnerID: "former", Lower: 10, Upper: 0, Closed: true, Evidence: []SourceRef{ref}}, HistoryPolicySHA256: strings.Repeat("d", 64), OriginalOwnerWindowsRootSHA256: strings.Repeat("e", 64), FloorEvidence: []SourceRef{ref}}
	cases := []struct {
		name         string
		lower, upper int64
		closed, want bool
	}{{"closed-empty", 10, 0, true, true}, {"closed-gap-does-not-expand", 10, 9, true, true}, {"open-floor", 10, 0, false, true}, {"open-cannot-carry-upper", 10, 9, false, false}, {"negative", -1, 0, true, false}, {"overflow", 9223372036854775807, 0, true, false}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := clone(base)
			v.Window.Lower = c.lower
			v.Window.Upper = c.upper
			v.Window.Closed = c.closed
			_, e := EffectiveBoundarySHA256(v)
			if (e == nil) != c.want {
				t.Fatalf("boundary shape accepted=%v wanted=%v", e == nil, c.want)
			}
		})
	}
}
func TestCoverageSetRefusesDuplicateOwnerAndSeparatesBoundary(t *testing.T) {
	owner := EffectiveOwnerKey("n_g", "u_with_underscore")
	other := EffectiveOwnerKey("n_other", "u_with_underscore")
	if owner == other {
		t.Fatal("different CID owner identity collided")
	}
	if _, e := SortedSetRoot([]string{owner, owner}); e == nil {
		t.Fatal("same owner counted twice")
	}
	one, e := SortedSetRoot([]string{owner, other})
	if e != nil {
		t.Fatal(e)
	}
	two, e := SortedSetRoot([]string{other, owner})
	if e != nil || one != two {
		t.Fatal("set root depends on page/order")
	}
}
