package rpcclient

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/encryption"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"google.golang.org/grpc"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

type Encryption struct {
	Client encryption.EncryptionClient
	conn   grpc.ClientConnInterface
	discov discoveryregistry.SvcDiscoveryRegistry
}

func NewEncryption(discov discoveryregistry.SvcDiscoveryRegistry) *Encryption {
	conn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImEncryptionName)
	if err != nil {
		panic(err)
	}
	client := encryption.NewEncryptionClient(conn)
	return &Encryption{discov: discov, conn: conn, Client: client}
}

type EncryptionRpcClient Encryption

func NewEncryptionRpcClient(discov discoveryregistry.SvcDiscoveryRegistry) EncryptionRpcClient {
	return EncryptionRpcClient(*NewEncryption(discov))
}

func (c *EncryptionRpcClient) GetEncryptionKey(ctx context.Context, userID, conversationID string) (int32, error) {
	//var req pbConversation.GetConversationReq
	//req.OwnerUserID = userID
	//req.ConversationID = conversationID
	//conversation, err := c.Client.GetConversation(ctx, &req)
	//if err != nil {
	//	return 0, err
	//}
	//return conversation.GetConversation().RecvMsgOpt, err
	return 0, nil
}

func (c *EncryptionRpcClient) GenEncryptionKey(ctx context.Context, conversationID string) error {
	// Key creation is an internal service operation; public endpoints expose
	// only participant-scoped reads. Preserve the triggering operationID.
	actor := ""
	for _, candidate := range append(append([]string{}, config.Config.Manager.UserID...), config.Config.IMAdmin.UserID...) {
		if candidate != "" {
			actor = candidate
			break
		}
	}
	if actor == "" {
		return errs.ErrNoPermission.Wrap("internal encryption administrator is not configured")
	}
	ctx = context.WithValue(ctx, constant.OpUserID, actor)
	_, err := c.Client.GenEncryptionKey(ctx, &encryption.GenEncryptionKeyReq{ConversationID: conversationID})
	return err
}
