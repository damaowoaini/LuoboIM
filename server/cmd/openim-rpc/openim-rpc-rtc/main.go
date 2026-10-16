package main

import (
	"github.com/openimsdk/open-im-server/v3/internal/rpc/rtc"
	"github.com/openimsdk/open-im-server/v3/pkg/common/cmd"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func main() {
	command := cmd.NewRpcCmd(cmd.RpcRtcServer)
	command.AddPortFlag()
	command.AddPrometheusPortFlag()
	if err := command.Exec(); err != nil {
		panic(err)
	}
	if err := command.StartSvr(config.Config.RpcRegisterName.OpenImRtcName, rtc.Start); err != nil {
		panic(err)
	}
}
