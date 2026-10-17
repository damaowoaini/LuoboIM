package api

import (
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/gin-gonic/gin"

	"github.com/OpenIMSDK/protocol/rtc"

	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type SignalApi rpcclient.Signal

func NewSignalApi(discov discoveryregistry.SvcDiscoveryRegistry) SignalApi {
	return SignalApi(*rpcclient.NewSignal(discov))
}

func (s *SignalApi) SignalGetRoomByGroupID(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalGetRoomByGroupID, s.Client, c)
}

func (s *SignalApi) SignalGetTokenByRoomID(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalGetTokenByRoomID, s.Client, c)
}

func (s *SignalApi) SignalGetRooms(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalGetRooms, s.Client, c)
}

func (s *SignalApi) GetSignalInvitationInfo(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.GetSignalInvitationInfo, s.Client, c)
}

func (s *SignalApi) GetSignalInvitationInfoStartApp(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.GetSignalInvitationInfoStartApp, s.Client, c)
}

func (s *SignalApi) SignalCreateMeeting(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalCreateMeeting, s.Client, c)
}

func (s *SignalApi) SignalJoinMeeting(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalJoinMeeting, s.Client, c)
}

func (s *SignalApi) SignalUpdateMeetingInfo(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalUpdateMeetingInfo, s.Client, c)
}

func (s *SignalApi) SignalCloseRoom(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalCloseRoom, s.Client, c)
}

func (s *SignalApi) SignalGetMeetings(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalGetMeetings, s.Client, c)
}

func (s *SignalApi) SignalOperateStream(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalOperateStream, s.Client, c)
}

func (s *SignalApi) SignalSendCustomSignal(c *gin.Context) {
	a2r.Call(rtc.RtcServiceClient.SignalSendCustomSignal, s.Client, c)
}
