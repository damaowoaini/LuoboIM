// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	v3config "github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/stretchr/testify/mock"
	"gotest.tools/assert"
)

// MockRootCmd is a mock type for the RootCmd type
type MockRootCmd struct {
	mock.Mock
}

func (m *MockRootCmd) Execute() error {
	args := m.Called()
	return args.Error(0)
}

func TestMsgGatewayCmd_GetPortFromConfig(t *testing.T) {
	previous := v3config.Config.LongConnSvr
	t.Cleanup(func() { v3config.Config.LongConnSvr = previous })
	v3config.Config.LongConnSvr.OpenImWsPort = []int{8080}
	v3config.Config.LongConnSvr.OpenImMessageGatewayPort = []int{8081}
	msgGatewayCmd := &MsgGatewayCmd{RootCmd: &RootCmd{}}
	tests := []struct {
		portType string
		want     int
	}{
		{constant.FlagWsPort, 8080},
		{constant.FlagPort, 8081},
		{"invalid", 0},
	}
	for _, tt := range tests {
		t.Run(tt.portType, func(t *testing.T) {
			got := msgGatewayCmd.GetPortFromConfig(tt.portType)
			assert.Equal(t, tt.want, got)
		})
	}
}
