package msgtransfer

import (
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
)

var errGroupEffectPlan = errors.New("invalid frozen group notification plan")

type groupEffectBranch struct {
	ID, EffectID, ConversationID, PayloadSHA256 string
	Payload                                     []byte
	Persist                                     bool
}

// Both the queue consumer and the transactional receiver use this SAME pure
// wire projection. The helper grants no durable-source or recipient authority.
func planGroupEffect(f groupeffects.Frozen) ([]groupEffectBranch, error) {
	plans, err := groupeffects.PlanNotificationBranches(f)
	if err != nil {
		return nil, err
	}
	result := make([]groupEffectBranch, 0, len(plans))
	for _, p := range plans {
		result = append(result, groupEffectBranch{ID: p.ID, EffectID: p.EffectID, ConversationID: p.ConversationID, PayloadSHA256: p.PayloadSHA256, Payload: append([]byte(nil), p.Payload...), Persist: p.Persist})
	}
	return result, nil
}
