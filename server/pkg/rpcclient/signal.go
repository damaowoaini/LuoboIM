package rpcclient

import (
	"context"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"google.golang.org/grpc"

	"github.com/OpenIMSDK/protocol/rtc"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

type Signal struct {
	conn grpc.ClientConnInterface

	Client rtc.RtcServiceClient
	discov discoveryregistry.SvcDiscoveryRegistry
}

func NewSignal(discov discoveryregistry.SvcDiscoveryRegistry) *Signal {
	conn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImRtcName)
	if err != nil {
		panic(err)
	}
	client := rtc.NewRtcServiceClient(conn)
	return &Signal{discov: discov, conn: conn, Client: client}
}

type SignalRpcClient Signal

func NewSignalRpcClient(discov discoveryregistry.SvcDiscoveryRegistry) SignalRpcClient {
	return SignalRpcClient(*NewSignal(discov))
}
