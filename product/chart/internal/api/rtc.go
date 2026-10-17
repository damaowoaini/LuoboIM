package api

import (
	"github.com/OpenIMSDK/chat/pkg/proto/rtc"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/gin-gonic/gin"
)

func (o *AdminApi) DeleteSignalRecords(c *gin.Context) {
	attachRTCAdminCredential(c)
	a2r.Call(rtc.RtcServiceClient.DeleteSignalRecords, o.rtcClient, c)
}

func (o *AdminApi) GetSignalInvitationRecords(c *gin.Context) {
	attachRTCAdminCredential(c)
	a2r.Call(rtc.RtcServiceClient.GetSignalInvitationRecords, o.rtcClient, c)
}

func (o *AdminApi) GetMeetingRecords(c *gin.Context) {
	attachRTCAdminCredential(c)
	a2r.Call(rtc.RtcServiceClient.GetMeetingRecords, o.rtcClient, c)
}

func (o *AdminApi) DeleteMeetingRecords(c *gin.Context) {
	attachRTCAdminCredential(c)
	a2r.Call(rtc.RtcServiceClient.DeleteMeetingRecords, o.rtcClient, c)
}

func attachRTCAdminCredential(c *gin.Context) {
	const key = "x-luobo-chart-admin-token"
	headers, _ := c.Value(constant.RpcCustomHeader).([]string)
	c.Set(constant.RpcCustomHeader, append(append([]string{}, headers...), key))
	c.Set(key, []string{c.GetHeader("token")})
}
