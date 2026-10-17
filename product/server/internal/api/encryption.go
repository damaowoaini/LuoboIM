package api

import (
	"github.com/gin-gonic/gin"

	"github.com/OpenIMSDK/protocol/encryption"
	"github.com/OpenIMSDK/tools/a2r"

	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type EncryptionApi rpcclient.Encryption

func NewEncryptionApi(client rpcclient.Encryption) EncryptionApi {
	return EncryptionApi(client)
}

func (e *EncryptionApi) GetEncryptionKey(c *gin.Context) {
	a2r.Call(encryption.EncryptionClient.GetEncryptionKey, e.Client, c)
}
