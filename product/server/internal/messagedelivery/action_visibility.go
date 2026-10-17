package messagedelivery

import (
	"context"
	"errors"
)

var ErrOwnedActionRedacted = errors.New("owned action notification redacted before attempt")

func (in ownedActionInput) denied(ctx context.Context, fence bool) (map[string][]string, bool, error) {
	if in.store == nil {
		return nil, false, ErrUnprepared
	}
	receipt, accepted, err := in.store.ownedAccepted(ctx, in.frozen.ID)
	if err != nil {
		return nil, false, err
	}
	sequences := map[string]int64{}
	normalSequence := int64(0)
	if accepted {
		for _, b := range receipt.Branches {
			sequences[b.ID] = b.Seq
			if b.ConversationID == in.cap.VisibilitySnapshot().ConversationID && b.Persist {
				normalSequence = b.Seq
			}
		}
	}
	denied := map[string][]string{}
	all := true
	count := 0
	for _, target := range in.targets {
		for start := 0; start < len(target.UserIDs); start += 64 {
			page := target.UserIDs[start:min(start+64, len(target.UserIDs))]
			result, err := in.effects.ValidateOwnedVisibility(ctx, in.cap, target.BranchID, page, sequences[target.BranchID], normalSequence, fence)
			if err != nil {
				return nil, false, err
			}
			for _, uid := range page {
				count++
				if result[uid] {
					denied[target.BranchID] = append(denied[target.BranchID], uid)
				} else {
					all = false
				}
			}
		}
	}
	return denied, count > 0 && all, nil
}
func receiptAllHidden(r Receipt) bool {
	if len(r.Branches) == 0 {
		return false
	}
	for _, b := range r.Branches {
		if !b.Hidden {
			return false
		}
	}
	return true
}
