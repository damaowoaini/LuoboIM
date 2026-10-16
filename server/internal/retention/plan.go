package retention

import (
	"context"
	"math"
	"time"
)

type Source interface {
	Slots(context.Context, Policy, int64, int) ([]Slot, error)
}

// BuildPlan is pure with respect to stored state. It only advances a safe
// contiguous prefix; an expired later message cannot hide an earlier live one.
func BuildPlan(ctx context.Context, source Source, policy Policy, floor int64, now time.Time, limit int) (Plan, error) {
	cutoff, err := policy.Cutoff(now)
	if err != nil || floor < 0 || limit < 1 || limit > MaximumChunk {
		return Plan{}, ErrInvalidPolicy
	}
	if floor == 0 {
		floor = 1
	}
	plan := Plan{Policy: policy, FromFloor: floor, ToFloor: floor, At: now, Limit: limit}
	if floor > math.MaxInt64-int64(limit) {
		return Plan{}, ErrInvalidPolicy
	}
	slots, err := source.Slots(ctx, policy, floor, limit)
	if err != nil {
		return Plan{}, err
	} // No partial destructive plan on I/O error.
	if len(slots) > limit {
		return Plan{}, ErrSourceChanged
	}
	seenDocs := map[string]bool{}
	for i, slot := range slots {
		if slot.Seq != floor+int64(i) {
			return Plan{}, ErrSourceChanged
		}
		if !slot.Known {
			break
		} // MQ/Redis and sparse late persistence are not EOF.
		if slot.Document == "" {
			return Plan{}, ErrSourceChanged
		}
		if !slot.Hidden && (slot.SendTime <= 0 || slot.SendTime > cutoff) {
			break
		}
		plan.ToFloor = slot.Seq + 1
		plan.Sequences = append(plan.Sequences, slot.Seq)
		if !seenDocs[slot.Document] {
			seenDocs[slot.Document] = true
			plan.Documents = append(plan.Documents, slot.Document)
		}
	}
	return plan, nil
}

func samePlan(a, b Plan) bool {
	if a.FromFloor != b.FromFloor || a.ToFloor != b.ToFloor || len(a.Sequences) != len(b.Sequences) || len(a.Documents) != len(b.Documents) {
		return false
	}
	for i := range a.Sequences {
		if a.Sequences[i] != b.Sequences[i] {
			return false
		}
	}
	for i := range a.Documents {
		if a.Documents[i] != b.Documents[i] {
			return false
		}
	}
	return true
}
