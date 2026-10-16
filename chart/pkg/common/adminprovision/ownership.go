package adminprovision

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/tools/errs"
	"gorm.io/gorm"
)

// RequireReady protects the SQL→IM ownership boundary. Missing outbox rows are
// legacy accounts and keep their previous behavior; storage errors fail closed.
// A persisted conflict must never become an ordinary user's IM credential.
func RequireReady(ctx context.Context, db *gorm.DB, id string) error {
	if db == nil || id == "" {
		return errs.ErrNoPermission.Wrap("账号归属无法确认")
	}
	var job Job
	e := db.WithContext(ctx).Select("state", "im_provisioned_at", "route_required", "route_bound_at").Where("user_id=?", id).Take(&job).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		var receipt registration.Receipt
		lookup := db.WithContext(ctx).Select("im_provisioned_at").Where("user_id=?", id).Take(&receipt).Error
		if errors.Is(lookup, gorm.ErrRecordNotFound) {
			return nil
		}
		if lookup != nil {
			return errs.ErrNoPermission.Wrap("账号归属状态暂不可用")
		}
		if receipt.IMProvisionedAt == nil {
			return errs.ErrNoPermission.Wrap("账号正在开通或归属待核验，请联系管理员")
		}
		return nil
	}
	if e != nil {
		return errs.ErrNoPermission.Wrap("账号归属状态暂不可用")
	}
	if (job.State != "bound" && job.State != "direct_only") || job.IMProvisionedAt == nil || (job.State == "bound" && job.RouteBoundAt == nil) || (job.State == "direct_only" && job.RouteRequired) {
		return errs.ErrNoPermission.Wrap("账号正在开通或归属待核验，请联系管理员")
	}
	return nil
}
func (s *Service) RequireReady(ctx context.Context, id string) error {
	return RequireReady(ctx, s.DB, id)
}
