package messagedelivery

import (
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
)

// The former reaction mutator DTOs have no mounted authenticated implementation.
// Preserve their decode surface, but never pretend a generic SendMsg applied
// such a mutation. This is NOT a runtime-wide readiness switch.
var ErrUnsupportedModifier = errors.New("unsupported message modifier content type")

func CheckSupportedMessageContentType(kind int32) error {
	if kind == constant.ReactionMessageModifier || kind == constant.ReactionMessageDeleter {
		return ErrUnsupportedModifier
	}
	return nil
}
