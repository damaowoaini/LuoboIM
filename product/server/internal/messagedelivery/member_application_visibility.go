package messagedelivery

import (
	"context"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
)

// Called after the original authenticated writer and BumpVisibilityCut in the
// SAME strong transaction. Reuse the existing controlled batch refusal; never
// commit a floor after retiring only a prefix of matching source plaintext.
func (s *Store) redactMemberApplicationSources(ctx context.Context, change table.PendingVisibilityChange) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return err
	}
	err = effects.RedactMemberApplicationSources(ctx, change)
	if errors.Is(err, groupeffects.ErrMemberApplicationRedactionNeedsBatch) {
		return ErrRedactionNeedsBatch
	}
	return err
}
