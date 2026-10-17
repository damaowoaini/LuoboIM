package messagedelivery

import (
	"math"
	"testing"
)

func TestOwnerHistorySegmentsPreserveBaseGapFutureAndRevocation(t *testing.T) {
	base := ownerHistorySegments{BaseLower: 2, BaseUpper: 5, BaseClosed: true, SeedMaximum: 10, FutureLower: 11, FutureUpper: 14}
	for _, name := range []string{"base", "gap", "future", "no-positive-source", "closed-empty", "owner-clear", "global-clear", "forbid-history-rejoin", "independent-notification-head", "Seq0"} {
		t.Run(name, func(t *testing.T) {
			p := base
			seq := int64(3)
			want := true
			switch name {
			case "gap":
				seq = 6
				want = false
			case "future":
				seq = 11
			case "no-positive-source":
				p.FutureUpper = 0
				seq = 11
				want = false
			case "closed-empty":
				p.BaseUpper = 0
				seq = 2
				want = false
			case "owner-clear":
				p.Floor = 4
				seq = 3
				want = false
			case "global-clear":
				p.Floor = 15
				seq = 14
				want = false
			case "forbid-history-rejoin":
				p.HistoricalFloor = 15
				seq = 3
				want = false
			case "independent-notification-head":
				p.SeedMaximum = 20
				p.FutureLower = 21
				p.FutureUpper = 24
				seq = 11
				want = false
			case "Seq0":
				seq = 0
				want = false
			}
			if !p.valid() || p.allows(seq) != want {
				t.Fatalf("%s allows(%d)=%v want %v", name, seq, p.allows(seq), want)
			}
			if p.allows(0) || p.allows(math.MaxInt64) {
				t.Fatal("non-history sequence granted")
			}
			if name == "forbid-history-rejoin" {
				for _, old := range []int64{2, 5, 11, 14} {
					if p.allows(old) {
						t.Fatal("immutable base/future resurrected after real revocation", old)
					}
				}
			}
		})
	}
}
func TestOwnerHistorySegmentsRejectInventedGapOrInvalidBounds(t *testing.T) {
	for _, name := range []string{"foreign-future-lower", "negative-floor", "future-before-seed", "overflow-head"} {
		t.Run(name, func(t *testing.T) {
			p := ownerHistorySegments{BaseLower: 2, BaseUpper: 5, BaseClosed: true, SeedMaximum: 10, FutureLower: 11, FutureUpper: 14}
			switch name {
			case "foreign-future-lower":
				p.FutureLower = 6
			case "negative-floor":
				p.HistoricalFloor = -1
			case "future-before-seed":
				p.FutureUpper = 9
			case "overflow-head":
				p.SeedMaximum = math.MaxInt64
			}
			if p.valid() || p.allows(3) {
				t.Fatal("invalid prospective segment granted")
			}
		})
	}
}
