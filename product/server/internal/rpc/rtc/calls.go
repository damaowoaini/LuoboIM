package rtc

import (
	"context"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

func platform(ctx context.Context) int32 {
	value, _ := ctx.Value(constant.OpUserPlatform).(string)
	for id, name := range constant.PlatformID2Name {
		if strings.EqualFold(value, name) {
			return int32(id)
		}
	}
	return 0
}
func (s *Server) lease(ctx context.Context, user, room string, expires time.Time) (bool, error) {
	var existing struct {
		RoomID    string    `bson:"room_id"`
		ExpiresAt time.Time `bson:"expires_at"`
	}
	err := s.active.FindOne(ctx, bson.M{"_id": user}).Decode(&existing)
	if err != nil && err != mongo.ErrNoDocuments {
		return false, err
	}
	if err == nil {
		if existing.RoomID == room {
			return true, nil
		}
		if existing.ExpiresAt.After(s.now()) {
			return false, nil
		}
		if _, err := s.active.DeleteOne(ctx, bson.M{"_id": user, "room_id": existing.RoomID}); err != nil {
			return false, err
		}
	}
	_, err = s.active.InsertOne(ctx, bson.M{"_id": user, "room_id": room, "expires_at": expires})
	return err == nil, err
}
func (s *Server) SignalMessageAssemble(ctx context.Context, req *pb.SignalMessageAssembleReq) (*pb.SignalMessageAssembleResp, error) {
	if req == nil || req.SignalReq == nil {
		return nil, argument("RTC信令为空")
	}
	var response *pb.SignalResp
	var err error
	switch request := req.SignalReq.Payload.(type) {
	case *pb.SignalReq_Invite:
		if request == nil || request.Invite == nil {
			return nil, argument("RTC信令内容为空")
		}
		response, err = s.invite(ctx, request.Invite.Invitation, request.Invite.OfflinePushInfo, request.Invite.UserID, false)
	case *pb.SignalReq_InviteInGroup:
		if request == nil || request.InviteInGroup == nil {
			return nil, argument("RTC信令内容为空")
		}
		response, err = s.invite(ctx, request.InviteInGroup.Invitation, request.InviteInGroup.OfflinePushInfo, request.InviteInGroup.UserID, true)
	case *pb.SignalReq_Accept:
		if request == nil || request.Accept == nil {
			return nil, argument("RTC信令内容为空")
		}
		response, err = s.changeCall(ctx, "accept", request.Accept.UserID, request.Accept.Invitation)
	case *pb.SignalReq_Reject:
		if request == nil || request.Reject == nil {
			return nil, argument("RTC信令内容为空")
		}
		response, err = s.changeCall(ctx, "reject", request.Reject.UserID, request.Reject.Invitation)
	case *pb.SignalReq_Cancel:
		if request == nil || request.Cancel == nil {
			return nil, argument("RTC信令内容为空")
		}
		response, err = s.changeCall(ctx, "cancel", request.Cancel.UserID, request.Cancel.Invitation)
	case *pb.SignalReq_HungUp:
		if request == nil || request.HungUp == nil {
			return nil, argument("RTC信令内容为空")
		}
		response, err = s.changeCall(ctx, "hungup", request.HungUp.UserID, request.HungUp.Invitation)
	case *pb.SignalReq_GetTokenByRoomID:
		if request == nil || request.GetTokenByRoomID == nil {
			return nil, argument("RTC信令内容为空")
		}
		token, tokenErr := s.SignalGetTokenByRoomID(ctx, request.GetTokenByRoomID)
		err = tokenErr
		if token != nil {
			response = &pb.SignalResp{Payload: &pb.SignalResp_GetTokenByRoomID{GetTokenByRoomID: token}}
		}
	default:
		err = argument("未知RTC信令")
	}
	if err != nil {
		return nil, err
	}
	return &pb.SignalMessageAssembleResp{SignalResp: response}, nil
}
func (s *Server) invite(ctx context.Context, requested *pb.InvitationInfo, push *sdkws.OfflinePushInfo, claimed string, group bool) (*pb.SignalResp, error) {
	id, err := actor(ctx, claimed)
	if err != nil {
		return nil, err
	}
	if requested == nil || requested.InviterUserID != "" && requested.InviterUserID != id {
		return nil, deny("RTC邀请者身份不匹配")
	}
	invitation := proto.Clone(requested).(*pb.InvitationInfo)
	if invitation.RoomID == "" {
		invitation.RoomID = uuid.NewString()
	}
	if !validRoomID(invitation.RoomID) || len(invitation.CustomData) > 8192 || len(invitation.GroupID) > 64 || proto.Size(push) > 16384 {
		return nil, argument("RTC邀请参数超出范围")
	}
	if invitation.MediaType != "audio" && invitation.MediaType != "video" {
		return nil, argument("RTC媒体类型无效")
	}
	if group {
		if invitation.GroupID == "" || invitation.SessionType != constant.SuperGroupChatType && invitation.SessionType != constant.GroupChatType {
			return nil, argument("群RTC邀请参数无效")
		}
	} else if invitation.GroupID != "" || invitation.SessionType != constant.SingleChatType {
		return nil, argument("单聊RTC邀请参数无效")
	}
	invitees, err := uniqueUsers(invitation.InviteeUserIDList)
	if err != nil || len(invitees) == 0 {
		return nil, argument("RTC被邀请人不能为空或重复")
	}
	for _, invitee := range invitees {
		if invitee == id {
			return nil, argument("不能邀请自己")
		}
	}
	if invitation.Timeout == 0 {
		invitation.Timeout = 30
	}
	if invitation.Timeout < 5 || invitation.Timeout > 120 {
		return nil, argument("RTC邀请超时须为5到120秒")
	}
	if platform(ctx) == 0 {
		return nil, deny("RTC平台身份缺失")
	}
	invitation.InviterUserID = id
	invitation.PlatformID = platform(ctx)
	invitation.InitiateTime = s.now().Unix()
	invitation.BusyLineUserIDList = nil
	if err := s.access.Invitees(ctx, id, invitation.GroupID, invitees); err != nil {
		return nil, err
	}
	metadata, err := s.access.Metadata(ctx, id, invitation.GroupID)
	if err != nil {
		return nil, err
	}
	metadataBytes, _ := proto.Marshal(metadata)
	pushBytes, _ := proto.Marshal(push)
	value, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		invitation := proto.Clone(invitation).(*pb.InvitationInfo)
		invitation.BusyLineUserIDList = nil
		existing, loadErr := s.load(tx, invitation.RoomID)
		if loadErr == nil {
			old, _ := existing.invitation()
			if existing.Kind != "call" || existing.Host != id || old == nil || !sameInvitation(old, invitation) {
				return nil, deny("RTC房间ID已由其他邀请使用")
			}
			if existing.terminal() {
				return nil, argument("RTC邀请已结束")
			}
			return existing, nil
		}
		if loadErr != nil && !isMissing(loadErr) {
			return nil, loadErr
		}
		deadline := s.now().Add(time.Duration(invitation.Timeout) * time.Second)
		ok, err := s.lease(tx, "user:"+id, invitation.RoomID, deadline)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, argument("你正在其他通话中")
		}
		if group {
			ok, err := s.lease(tx, "group:"+invitation.GroupID, invitation.RoomID, deadline)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, argument("群内已有进行中的通话")
			}
		}
		room := &Room{RoomID: invitation.RoomID, Kind: "call", Host: id, GroupID: invitation.GroupID, State: roomRinging, Version: 1, OfflinePush: pushBytes, CreatedAt: s.now(), ExpiresAt: deadline, Participants: []Participant{{UserID: id, Status: "accepted", PlatformID: platform(ctx), Metadata: metadataBytes, UpdatedAt: s.now()}}}
		for _, invitee := range invitees {
			ok, err := s.lease(tx, "user:"+invitee, room.RoomID, deadline)
			if err != nil {
				return nil, err
			}
			status := "invited"
			if !ok {
				status = "busy"
				invitation.BusyLineUserIDList = append(invitation.BusyLineUserIDList, invitee)
			}
			room.Participants = append(room.Participants, Participant{UserID: invitee, Status: status, UpdatedAt: s.now()})
		}
		room.Invitation, _ = proto.Marshal(invitation)
		if len(invitation.BusyLineUserIDList) == len(invitees) {
			if err := s.end(tx, room, roomEnded); err != nil {
				return nil, err
			}
		}
		if _, err := s.rooms.InsertOne(tx, room); err != nil {
			return nil, err
		}
		receivers := []string{}
		for _, p := range room.Participants {
			if p.Status == "invited" {
				receivers = append(receivers, p.UserID)
			}
		}
		if len(receivers) > 0 {
			notification := &pb.SignalReq{}
			if group {
				notification.Payload = &pb.SignalReq_InviteInGroup{InviteInGroup: &pb.SignalInviteInGroupReq{Invitation: invitation, OfflinePushInfo: push, Participant: metadata, UserID: id}}
			} else {
				notification.Payload = &pb.SignalReq_Invite{Invite: &pb.SignalInviteReq{Invitation: invitation, OfflinePushInfo: push, Participant: metadata, UserID: id}}
			}
			if err := s.enqueue(tx, room, id, constant.SignalingNotification, notification, receivers, pushBytes); err != nil {
				return nil, err
			}
		}
		return room, nil
	})
	if err != nil {
		return nil, err
	}
	room := value.(*Room)
	invitation, _ = room.invitation()
	token := ""
	if !room.terminal() {
		token, err = s.certificate(room, id)
		if err != nil {
			return nil, err
		}
	}
	live := s.liveURL()
	if group {
		return &pb.SignalResp{Payload: &pb.SignalResp_InviteInGroup{InviteInGroup: &pb.SignalInviteInGroupResp{Token: token, RoomID: room.RoomID, LiveURL: live, BusyLineUserIDList: invitation.BusyLineUserIDList}}}, nil
	}
	return &pb.SignalResp{Payload: &pb.SignalResp_Invite{Invite: &pb.SignalInviteResp{Token: token, RoomID: room.RoomID, LiveURL: live, BusyLineUserIDList: invitation.BusyLineUserIDList}}}, nil
}
func sameInvitation(a, b *pb.InvitationInfo) bool {
	if a.InviterUserID != b.InviterUserID || a.GroupID != b.GroupID || a.SessionType != b.SessionType || a.MediaType != b.MediaType || len(a.InviteeUserIDList) != len(b.InviteeUserIDList) {
		return false
	}
	for i, id := range a.InviteeUserIDList {
		if id != b.InviteeUserIDList[i] {
			return false
		}
	}
	return true
}
func (s *Server) liveURL() string {
	if media, ok := s.media.(interface{ URL() string }); ok {
		return media.URL()
	}
	return ""
}
func (l *LiveKit) URL() string { return l.LiveURL }

func (s *Server) changeCall(ctx context.Context, action, claimed string, requested *pb.InvitationInfo) (*pb.SignalResp, error) {
	id, err := actor(ctx, claimed)
	if err != nil {
		return nil, err
	}
	if requested == nil || !validRoomID(requested.RoomID) {
		return nil, argument("RTC房间ID无效")
	}
	if platform(ctx) == 0 {
		return nil, deny("RTC平台身份缺失")
	}
	var metadata *pb.ParticipantMetaData
	if action == "accept" {
		metadata, err = s.access.Metadata(ctx, id, requested.GroupID)
		if err != nil {
			return nil, err
		}
	}
	result, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, requested.RoomID)
		if err != nil {
			return nil, err
		}
		if room.Kind != "call" {
			return nil, argument("房间不是通话邀请")
		}
		inv, err := room.invitation()
		if err != nil {
			return nil, err
		}
		if requested.InviterUserID != "" && requested.InviterUserID != room.Host || requested.GroupID != room.GroupID {
			return nil, deny("RTC邀请与原房间不匹配")
		}
		participant := room.participant(id)
		if participant == nil || participant.Status == "busy" {
			return nil, deny("未被邀请参与此通话")
		}
		if room.terminal() {
			if action == "cancel" && id == room.Host || action == "hungup" || action == "reject" && participant.Status == "rejected" {
				return room, nil
			}
			return nil, argument("RTC通话已结束")
		}
		previous := room.Version
		notification := &pb.SignalReq{}
		switch action {
		case "accept":
			if id == room.Host {
				return nil, deny("邀请者不能接受自己的邀请")
			}
			if participant.Status == "accepted" || participant.Status == "joined" {
				if participant.PlatformID != platform(ctx) {
					return nil, argument("此通话已由你的其他终端接听")
				}
				return room, nil
			}
			if participant.Status != "invited" || s.now().After(time.Unix(inv.InitiateTime, 0).Add(time.Duration(inv.Timeout)*time.Second)) {
				return nil, argument("邀请已失效")
			}
			if room.GroupID != "" {
				if _, err := s.access.Metadata(tx, id, room.GroupID); err != nil {
					return nil, err
				}
			}
			data, _ := proto.Marshal(metadata)
			participant.Status = "accepted"
			participant.PlatformID = platform(ctx)
			participant.Metadata = data
			participant.UpdatedAt = s.now()
			room.State = roomActive
			room.ExpiresAt = s.now().Add(24 * time.Hour)
			if _, err := s.active.UpdateMany(tx, bson.M{"room_id": room.RoomID}, bson.M{"$set": bson.M{"expires_at": room.ExpiresAt}}); err != nil {
				return nil, err
			}
			notification.Payload = &pb.SignalReq_Accept{Accept: &pb.SignalAcceptReq{Invitation: inv, Participant: metadata, UserID: id, OpUserPlatformID: platform(ctx)}}
		case "reject":
			if id == room.Host {
				return nil, deny("邀请者不能拒绝自己的邀请")
			}
			if participant.Status == "rejected" {
				return room, nil
			}
			if participant.Status != "invited" {
				return nil, argument("邀请已由其他终端处理")
			}
			participant.Status = "rejected"
			participant.PlatformID = platform(ctx)
			participant.UpdatedAt = s.now()
			if _, err := s.active.DeleteOne(tx, bson.M{"_id": "user:" + id, "room_id": room.RoomID}); err != nil {
				return nil, err
			}
			waiting := false
			for _, p := range room.Participants {
				if p.UserID != room.Host && (p.Status == "invited" || p.Status == "accepted" || p.Status == "joined") {
					waiting = true
				}
			}
			if !waiting {
				if err := s.end(tx, room, roomEnded); err != nil {
					return nil, err
				}
			}
			notification.Payload = &pb.SignalReq_Reject{Reject: &pb.SignalRejectReq{Invitation: inv, UserID: id, OpUserPlatformID: platform(ctx)}}
		case "cancel":
			if id != room.Host {
				return nil, deny("只有邀请者可以取消通话")
			}
			if room.State != roomRinging {
				return nil, argument("通话已被接听，不能取消邀请")
			}
			if err := s.end(tx, room, roomCancelled); err != nil {
				return nil, err
			}
			notification.Payload = &pb.SignalReq_Cancel{Cancel: &pb.SignalCancelReq{Invitation: inv, UserID: id}}
		case "hungup":
			if participant.Status == "left" {
				return room, nil
			}
			if participant.Status != "accepted" && participant.Status != "joined" {
				return nil, deny("尚未加入此通话")
			}
			if participant.PlatformID != platform(ctx) {
				return nil, deny("通话属于你的其他终端")
			}
			participant.Status = "left"
			participant.UpdatedAt = s.now()
			if _, err := s.active.DeleteOne(tx, bson.M{"_id": "user:" + id, "room_id": room.RoomID}); err != nil {
				return nil, err
			}
			active := 0
			for _, p := range room.Participants {
				if p.Status == "accepted" || p.Status == "joined" {
					active++
				}
			}
			if room.GroupID == "" || active == 0 {
				if err := s.end(tx, room, roomEnded); err != nil {
					return nil, err
				}
			}
			notification.Payload = &pb.SignalReq_HungUp{HungUp: &pb.SignalHungUpReq{Invitation: inv, UserID: id}}
		default:
			return nil, argument("未知RTC状态操作")
		}
		if err := s.save(tx, room, previous); err != nil {
			return nil, err
		}
		if err := s.enqueue(tx, room, id, constant.SignalingNotification, notification, room.recipients(), nil); err != nil {
			return nil, err
		}
		if room.terminal() {
			if err := s.enqueueMedia(tx, room, "delete_room", "", "", false); err != nil {
				return nil, err
			}
		}
		return room, nil
	})
	if err != nil {
		return nil, err
	}
	room := result.(*Room)
	switch action {
	case "accept":
		token, err := s.certificate(room, id)
		if err != nil {
			return nil, err
		}
		return &pb.SignalResp{Payload: &pb.SignalResp_Accept{Accept: &pb.SignalAcceptResp{Token: token, RoomID: room.RoomID, LiveURL: s.liveURL()}}}, nil
	case "reject":
		return &pb.SignalResp{Payload: &pb.SignalResp_Reject{Reject: &pb.SignalRejectResp{}}}, nil
	case "cancel":
		return &pb.SignalResp{Payload: &pb.SignalResp_Cancel{Cancel: &pb.SignalCancelResp{}}}, nil
	default:
		return &pb.SignalResp{Payload: &pb.SignalResp_HungUp{HungUp: &pb.SignalHungUpResp{}}}, nil
	}
}
func (s *Server) SignalGetTokenByRoomID(ctx context.Context, req *pb.SignalGetTokenByRoomIDReq) (*pb.SignalGetTokenByRoomIDResp, error) {
	id, err := actor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if platform(ctx) == 0 {
		return nil, deny("RTC平台身份缺失")
	}
	result, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, req.RoomID)
		if err != nil {
			return nil, err
		}
		if room.Kind == "meeting" {
			return nil, argument("会议请使用加入会议接口")
		}
		if room.terminal() || !room.ExpiresAt.After(s.now()) {
			return nil, argument("通话已结束")
		}
		participant := room.participant(id)
		if room.GroupID != "" {
			meta, err := s.access.Metadata(tx, id, room.GroupID)
			if err != nil {
				return nil, err
			}
			if participant == nil {
				data, _ := proto.Marshal(meta)
				room.Participants = append(room.Participants, Participant{UserID: id, Status: "accepted", PlatformID: platform(ctx), Metadata: data, UpdatedAt: s.now()})
				participant = room.participant(id)
			} else if participant.Status == "invited" || participant.Status == "left" || participant.Status == "rejected" || participant.Status == "timeout" || participant.Status == "busy" {
				data, _ := proto.Marshal(meta)
				participant.Status = "accepted"
				participant.PlatformID = platform(ctx)
				participant.Metadata = data
				participant.UpdatedAt = s.now()
			}
		}
		if participant == nil || participant.Status != "accepted" && participant.Status != "joined" {
			return nil, deny("尚未接听此通话")
		}
		if participant.PlatformID != platform(ctx) {
			return nil, deny("此通话已由你的其他终端接听")
		}
		if room.GroupID != "" {
			ok, err := s.lease(tx, "user:"+id, room.RoomID, s.now().Add(24*time.Hour))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, argument("你正在其他通话中")
			}
			previous := room.Version
			room.State = roomActive
			room.ExpiresAt = s.now().Add(24 * time.Hour)
			if err := s.save(tx, room, previous); err != nil {
				return nil, err
			}
		}
		return room, nil
	})
	if err != nil {
		return nil, err
	}
	room := result.(*Room)
	token, err := s.certificate(room, id)
	if err != nil {
		return nil, err
	}
	return &pb.SignalGetTokenByRoomIDResp{Token: token, LiveURL: s.liveURL()}, nil
}
func (s *Server) roomResponse(room *Room) (*pb.SignalGetRoomByGroupIDResp, error) {
	inv, err := room.invitation()
	if err != nil {
		return nil, err
	}
	return &pb.SignalGetRoomByGroupIDResp{Invitation: inv, Participant: room.metadata(), RoomID: room.RoomID}, nil
}
func (s *Server) SignalGetRoomByGroupID(ctx context.Context, req *pb.SignalGetRoomByGroupIDReq) (*pb.SignalGetRoomByGroupIDResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	if req.GroupID == "" {
		return nil, argument("群ID不能为空")
	}
	if _, err := s.access.Metadata(ctx, id, req.GroupID); err != nil {
		return nil, err
	}
	var room Room
	err = s.rooms.FindOne(ctx, bson.M{"group_id": req.GroupID, "kind": "call", "state": bson.M{"$in": bson.A{roomRinging, roomActive}}, "deleted_at": nil, "expires_at": bson.M{"$gt": s.now()}}).Decode(&room)
	if err == mongo.ErrNoDocuments {
		return &pb.SignalGetRoomByGroupIDResp{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.roomResponse(&room)
}
func (s *Server) SignalGetRooms(ctx context.Context, req *pb.SignalGetRoomsReq) (*pb.SignalGetRoomsResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	if len(req.RoomIDs) > 100 {
		return nil, argument("单次最多查询100个RTC房间")
	}
	result := &pb.SignalGetRoomsResp{RoomList: []*pb.SignalGetRoomByGroupIDResp{}}
	for _, roomID := range req.RoomIDs {
		room, err := s.load(ctx, roomID)
		if err != nil {
			return nil, err
		}
		if room.participant(id) == nil {
			return nil, deny("无权查询RTC房间")
		}
		if room.GroupID != "" {
			if _, err := s.access.Metadata(ctx, id, room.GroupID); err != nil {
				return nil, err
			}
		}
		info, err := s.roomResponse(room)
		if err != nil {
			return nil, err
		}
		result.RoomList = append(result.RoomList, info)
	}
	return result, nil
}
func (s *Server) GetSignalInvitationInfo(ctx context.Context, req *pb.GetSignalInvitationInfoReq) (*pb.GetSignalInvitationInfoResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	room, err := s.load(ctx, req.RoomID)
	if err != nil {
		return nil, err
	}
	if room.participant(id) == nil {
		return nil, deny("无权查询RTC邀请")
	}
	if room.terminal() {
		return &pb.GetSignalInvitationInfoResp{}, nil
	}
	if room.GroupID != "" {
		if _, err := s.access.Metadata(ctx, id, room.GroupID); err != nil {
			return nil, err
		}
	}
	inv, err := room.invitation()
	if err != nil {
		return nil, err
	}
	push := &sdkws.OfflinePushInfo{}
	proto.Unmarshal(room.OfflinePush, push)
	return &pb.GetSignalInvitationInfoResp{InvitationInfo: inv, OfflinePushInfo: push}, nil
}
func (s *Server) GetSignalInvitationInfoStartApp(ctx context.Context, req *pb.GetSignalInvitationInfoStartAppReq) (*pb.GetSignalInvitationInfoStartAppResp, error) {
	id, err := actor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	var room Room
	err = s.rooms.FindOne(ctx, bson.M{"kind": "call", "participants": bson.M{"$elemMatch": bson.M{"user_id": id, "status": "invited"}}, "state": bson.M{"$in": bson.A{roomRinging, roomActive}}, "deleted_at": nil}, options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}})).Decode(&room)
	if err == mongo.ErrNoDocuments {
		return &pb.GetSignalInvitationInfoStartAppResp{}, nil
	}
	if err != nil {
		return nil, err
	}
	inv, err := room.invitation()
	if err != nil {
		return nil, err
	}
	if s.now().After(time.Unix(inv.InitiateTime, 0).Add(time.Duration(inv.Timeout) * time.Second)) {
		return &pb.GetSignalInvitationInfoStartAppResp{}, nil
	}
	if room.GroupID != "" {
		if _, err := s.access.Metadata(ctx, id, room.GroupID); err != nil {
			return nil, err
		}
	}
	push := &sdkws.OfflinePushInfo{}
	proto.Unmarshal(room.OfflinePush, push)
	return &pb.GetSignalInvitationInfoStartAppResp{Invitation: inv, OfflinePushInfo: push}, nil
}
