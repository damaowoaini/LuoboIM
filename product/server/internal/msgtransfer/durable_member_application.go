package msgtransfer

import (
	"context"
	"errors"
	"time"

	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func (w *ManagedGroupActionWorker) bindMemberApplicationRecovery(ctx context.Context, effects *groupeffects.Store, discovery discoveryregistry.SvcDiscoveryRegistry) error {
	if w == nil || ctx == nil || effects == nil || discovery == nil || w.memberApplications != nil || config.Config.RpcRegisterName.OpenImMsgName == "" {
		return messagedelivery.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	connection, err := discovery.GetConn(bounded, config.Config.RpcRegisterName.OpenImMsgName)
	cancel()
	if err != nil {
		return errors.New("owned member application message service unavailable")
	}
	// Discovery owns this original shared pool. Worker Stop joins before that
	// registry is closed by managed startup; no parallel owner/close is added.
	client := pbmsg.NewMsgClient(connection)
	cursor := ""
	w.memberApplications = func(ctx context.Context) error {
		page, err := effects.ListMemberApplicationDirect(ctx, cursor, 8)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			cursor = ""
			return nil
		}
		var failures []error
		for _, row := range page {
			if ctx.Err() != nil {
				return errors.Join(append(failures, ctx.Err())...)
			}
			cursor = row.Cursor
			err := effects.DeliverMemberApplicationDirect(ctx, row.ID, func(work context.Context, request *pbmsg.SendMsgReq) (*pbmsg.SendMsgResp, error) {
				return client.SendMsg(work, request)
			})
			if err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	return nil
}
