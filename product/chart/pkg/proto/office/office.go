package office

import (
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"net/url"
	"strings"
)

func (x *CreateOneWorkMomentReq) Check() error {
	if x == nil || x.Content == nil {
		return errs.ErrArgs.Wrap("内容不能为空")
	}
	if len(x.Content.Text) > 65536 || (strings.TrimSpace(x.Content.Text) == "" && len(x.Content.Metas) == 0) || x.Content.Type < 0 || x.Content.Type > 1 || len(x.Content.Metas) > 9 || (x.Content.Type == 1 && len(x.Content.Metas) != 1) {
		return errs.ErrArgs.Wrap("动态内容或附件无效")
	}
	if len(x.AtUserIDs) > 1000 || len(x.PermissionGroupIDs) > 100 || len(x.PermissionUserIDs) > 100000 {
		return errs.ErrArgs.Wrap("可见范围过大")
	}
	for _, meta := range x.Content.Metas {
		if meta == nil || !safeMomentMedia(meta.Original) || (meta.Thumb != "" && !safeMomentMedia(meta.Thumb)) || meta.Width < 0 || meta.Height < 0 || meta.Width > 100000 || meta.Height > 100000 {
			return errs.ErrArgs.Wrap("附件无效")
		}
	}
	return nil
}

func safeMomentMedia(value string) bool {
	if len(value) > 8192 {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func (x *FindRelevantWorkMomentsReq) Check() error {
	return checkMomentPagination(x.Pagination)
}

func (x *GetUserRecvWorkMomentsReq) Check() error { return checkMomentPagination(x.Pagination) }
func (x *GetUserSendWorkMomentsReq) Check() error { return checkMomentPagination(x.Pagination) }
func checkMomentPagination(p *sdkws.RequestPagination) error {
	if p == nil || p.PageNumber < 1 || p.PageNumber > 100000 || p.ShowNumber < 1 || p.ShowNumber > 100 {
		return errs.ErrArgs.Wrap("分页参数无效")
	}
	return nil
}
func (x *CommentOneWorkMomentReq) Check() error {
	if x.WorkMomentID == "" || strings.TrimSpace(x.Content) == "" || len(x.Content) > 8192 {
		return errs.ErrArgs.Wrap("评论内容无效")
	}
	return nil
}
