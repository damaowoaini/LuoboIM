package rtc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func (s *Server) SignalCreateMeeting(ctx context.Context, req *pb.SignalCreateMeetingReq) (*pb.SignalCreateMeetingResp, error) {
	if platform(ctx) == 0 {
		return nil, deny("RTC平台身份缺失")
	}
	id, err := actor(ctx, req.MeetingHostUserID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.MeetingName)
	if name == "" || len(name) > 256 || len(req.Ex) > 8192 {
		return nil, argument("会议名称为空或参数过长")
	}
	roomID := req.RoomID
	if roomID == "" {
		roomID = uuid.NewString()
	}
	if !validRoomID(roomID) {
		return nil, argument("会议房间ID无效")
	}
	users, err := uniqueUsers(req.InviteeUserIDList)
	if err != nil {
		return nil, err
	}
	filtered := []string{}
	for _, user := range users {
		if user != id {
			filtered = append(filtered, user)
		}
	}
	users = filtered
	if err := s.access.Invitees(ctx, id, "", users); err != nil {
		return nil, err
	}
	meta, err := s.access.Metadata(ctx, id, "")
	if err != nil {
		return nil, err
	}
	metadata, _ := proto.Marshal(meta)
	duration := req.MeetingDuration
	if duration == 0 {
		duration = 3600
	}
	if duration < 60 || duration > 7*24*3600 {
		return nil, argument("会议时长须为60秒至7天")
	}
	start := req.StartTime
	if start == 0 {
		start = s.now().Unix()
	}
	if start < s.now().Add(-time.Hour).Unix() || start > s.now().AddDate(1, 0, 0).Unix() {
		return nil, argument("会议开始时间无效")
	}
	meeting := &pb.MeetingInfo{RoomID: roomID, MeetingName: name, Ex: req.Ex, HostUserID: id, InviteeUserIDList: users, CreateTime: s.now().Unix(), StartTime: start, EndTime: start + duration, ParticipantCanUnmuteSelf: true, ParticipantCanEnableVideo: true, OnlyHostInviteUser: true, OnlyHostShareScreen: true, JoinDisableMicrophone: true, JoinDisableVideo: true}
	data, _ := proto.Marshal(meeting)
	result, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		if old, err := s.load(tx, roomID); err == nil {
			if old.Kind != "meeting" || old.Host != id {
				return nil, deny("会议房间ID已被使用")
			}
			if old.terminal() {
				return nil, argument("会议已结束")
			}
			return old, nil
		} else if !isMissing(err) {
			return nil, err
		}
		room := &Room{RoomID: roomID, Kind: "meeting", Host: id, State: roomActive, Version: 1, Meeting: data, CreatedAt: s.now(), ExpiresAt: time.Unix(meeting.EndTime, 0), Participants: []Participant{{UserID: id, Status: "accepted", PlatformID: platform(ctx), Metadata: metadata, UpdatedAt: s.now()}}}
		for _, user := range users {
			room.Participants = append(room.Participants, Participant{UserID: user, Status: "invited", UpdatedAt: s.now()})
		}
		// Meeting invitations are durable schedule access, rather than active call
		// reservations. A participant claims the busy lease only when joining.
		if _, err := s.rooms.InsertOne(tx, room); err != nil {
			return nil, err
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
	return &pb.SignalCreateMeetingResp{Token: token, RoomID: room.RoomID, LiveURL: s.liveURL()}, nil
}
func (s *Server) SignalJoinMeeting(ctx context.Context, req *pb.SignalJoinMeetingReq) (*pb.SignalJoinMeetingResp, error) {
	if platform(ctx) == 0 {
		return nil, deny("RTC平台身份缺失")
	}
	id, err := actor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	meta, err := s.access.Metadata(ctx, id, "")
	if err != nil {
		return nil, err
	}
	metadata, _ := proto.Marshal(meta)
	result, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, req.RoomID)
		if err != nil {
			return nil, err
		}
		if room.Kind != "meeting" || room.terminal() || !room.ExpiresAt.After(s.now()) {
			return nil, argument("会议已结束")
		}
		participant := room.participant(id)
		if participant == nil {
			return nil, deny("你未被邀请参加此会议")
		}
		if (participant.Status == "accepted" || participant.Status == "joined") && participant.PlatformID != 0 && participant.PlatformID != platform(ctx) {
			return nil, deny("会议已由你的其他终端加入")
		}
		ok, err := s.lease(tx, "user:"+id, room.RoomID, room.ExpiresAt)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, argument("你正在其他通话中")
		}
		previous := room.Version
		// Persist initial mute restrictions in the same authority used for tokens.
		// A permitted self-unmute removes the individual restriction even while
		// the meeting retains its default mute-all setting for future joiners.
		meeting, err := room.meeting()
		if err != nil {
			return nil, err
		}
		if participant.Status != "accepted" && participant.Status != "joined" {
			if meeting.JoinDisableMicrophone || meeting.IsMuteAllMicrophone {
				meeting.DisableMicrophoneUserIDList = union(meeting.DisableMicrophoneUserIDList, []string{id}, nil)
			}
			if meeting.JoinDisableVideo || meeting.IsMuteAllVideo {
				meeting.DisableVideoUserIDList = union(meeting.DisableVideoUserIDList, []string{id}, nil)
			}
			room.Meeting, _ = proto.Marshal(meeting)
		}
		participant.Status = "accepted"
		participant.PlatformID = platform(ctx)
		participant.Metadata = metadata
		participant.UpdatedAt = s.now()
		if err := s.save(tx, room, previous); err != nil {
			return nil, err
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
	return &pb.SignalJoinMeetingResp{Token: token, RoomID: room.RoomID, LiveURL: s.liveURL()}, nil
}
func union(existing, add, remove []string) []string {
	removed := map[string]bool{}
	for _, id := range remove {
		removed[id] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, id := range append(append([]string{}, existing...), add...) {
		if !removed[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}
func (s *Server) SignalUpdateMeetingInfo(ctx context.Context, req *pb.SignalUpdateMeetingInfoReq) (*pb.SignalUpdateMeetingInfoResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	if len(req.GetMeetingName().GetValue()) > 256 || len(req.GetEx().GetValue()) > 8192 {
		return nil, argument("会议资料超出长度限制")
	}
	_, err = s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, req.RoomID)
		if err != nil {
			return nil, err
		}
		if room.Kind != "meeting" || room.terminal() {
			return nil, argument("会议已结束")
		}
		meeting, err := room.meeting()
		if err != nil {
			return nil, err
		}
		if id != room.Host {
			if room.participant(id) == nil || meeting.OnlyHostInviteUser {
				return nil, deny("只有主持人可以修改会议")
			}
			clone := proto.Clone(req).(*pb.SignalUpdateMeetingInfoReq)
			clone.RoomID = ""
			clone.InviteeUserIDList = nil
			if proto.Size(clone) > 0 {
				return nil, deny("参会者只能邀请其他用户")
			}
		}
		invitees, err := uniqueUsers(req.InviteeUserIDList)
		if err != nil {
			return nil, err
		}
		if err := s.access.Invitees(tx, id, "", invitees); err != nil {
			return nil, err
		}
		meeting.InviteeUserIDList = union(meeting.InviteeUserIDList, invitees, nil)
		for _, user := range invitees {
			if room.participant(user) == nil {
				room.Participants = append(room.Participants, Participant{UserID: user, Status: "invited", UpdatedAt: s.now()})
			}
		}
		// Wrapped fields preserve the distinction between omitted and false/zero.
		source, destination := req.ProtoReflect(), meeting.ProtoReflect()
		for n := protoreflect.FieldNumber(2); n <= 14; n++ {
			field := source.Descriptor().Fields().ByNumber(n)
			if field == nil || field.IsList() || !source.Has(field) {
				continue
			}
			target := destination.Descriptor().Fields().ByName(field.Name())
			if target == nil {
				continue
			}
			wrapper := source.Get(field).Message()
			value := wrapper.Get(wrapper.Descriptor().Fields().ByNumber(1))
			destination.Set(target, value)
		}
		if req.IsMuteAllMicrophone != nil || req.IsMuteAllVideo != nil {
			ids := []string{}
			for _, participant := range room.Participants {
				if participant.UserID != room.Host {
					ids = append(ids, participant.UserID)
				}
			}
			if req.IsMuteAllMicrophone != nil {
				if meeting.IsMuteAllMicrophone {
					meeting.DisableMicrophoneUserIDList = union(meeting.DisableMicrophoneUserIDList, ids, nil)
				} else {
					meeting.DisableMicrophoneUserIDList = union(meeting.DisableMicrophoneUserIDList, nil, ids)
				}
			}
			if req.IsMuteAllVideo != nil {
				if meeting.IsMuteAllVideo {
					meeting.DisableVideoUserIDList = union(meeting.DisableVideoUserIDList, ids, nil)
				} else {
					meeting.DisableVideoUserIDList = union(meeting.DisableVideoUserIDList, nil, ids)
				}
			}
		}
		meeting.CanScreenUserIDList = updateList(meeting.CanScreenUserIDList, req.CanScreenUserIDList, req.AddCanScreenUserIDList, req.ReduceCanScreenUserIDList)
		meeting.DisableMicrophoneUserIDList = updateList(meeting.DisableMicrophoneUserIDList, req.DisableMicrophoneUserIDList, req.AddDisableMicrophoneUserIDList, req.ReduceDisableMicrophoneUserIDList)
		meeting.DisableVideoUserIDList = updateList(meeting.DisableVideoUserIDList, req.DisableVideoUserIDList, req.AddDisableVideoUserIDList, req.ReduceDisableVideoUserIDList)
		meeting.PinedUserIDList = updateList(meeting.PinedUserIDList, req.PinedUserIDList, req.AddPinedUserIDList, req.ReducePinedUserIDList)
		meeting.BeWatchedUserIDList = updateList(meeting.BeWatchedUserIDList, req.BeWatchedUserIDList, req.AddBeWatchedUserIDList, req.ReduceBeWatchedUserIDList)
		for _, list := range [][]string{meeting.CanScreenUserIDList, meeting.DisableMicrophoneUserIDList, meeting.DisableVideoUserIDList, meeting.PinedUserIDList, meeting.BeWatchedUserIDList} {
			if len(list) > 100 {
				return nil, argument("会议权限列表过长")
			}
			for _, user := range list {
				if room.participant(user) == nil {
					return nil, deny("权限列表包含非参会用户")
				}
			}
		}
		if strings.TrimSpace(meeting.MeetingName) == "" || meeting.EndTime <= meeting.StartTime || meeting.EndTime-meeting.StartTime > 7*24*3600 {
			return nil, argument("会议名称或起止时间无效")
		}
		previous := room.Version
		room.Meeting, _ = proto.Marshal(meeting)
		room.ExpiresAt = time.Unix(meeting.EndTime, 0)
		if err := s.save(tx, room, previous); err != nil {
			return nil, err
		}
		if err := s.enqueueMedia(tx, room, "permissions", "", "", false); err != nil {
			return nil, err
		}
		// The existing custom-signal event carries the complete meeting snapshot.
		data, _ := json.Marshal(map[string]any{"type": "meetingInfoUpdated", "meetingInfo": meeting, "revision": room.Version})
		if err := s.enqueue(tx, room, id, constant.CustomSignalNotification, &pb.SignalSendCustomSignalReq{RoomID: room.RoomID, CustomInfo: string(data)}, room.recipients(), nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SignalUpdateMeetingInfoResp{}, nil
}
func updateList(old, replace, add, remove []string) []string {
	if len(replace) > 0 {
		old = replace
	}
	return union(old, add, remove)
}
func (s *Server) SignalCloseRoom(ctx context.Context, req *pb.SignalCloseRoomReq) (*pb.SignalCloseRoomResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	_, err = s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, req.RoomID)
		if err != nil {
			return nil, err
		}
		if room.Host != id {
			return nil, deny("只有主持人可以关闭房间")
		}
		if room.terminal() {
			return nil, nil
		}
		previous := room.Version
		if err := s.end(tx, room, roomEnded); err != nil {
			return nil, err
		}
		if err := s.save(tx, room, previous); err != nil {
			return nil, err
		}
		if err := s.enqueueMedia(tx, room, "delete_room", "", "", false); err != nil {
			return nil, err
		}
		if err := s.enqueue(tx, room, id, constant.CustomSignalNotification, &pb.SignalSendCustomSignalReq{RoomID: room.RoomID, CustomInfo: `{"type":"roomClosed"}`}, room.recipients(), nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SignalCloseRoomResp{}, nil
}
func (s *Server) SignalGetMeetings(ctx context.Context, req *pb.SignalGetMeetingsReq) (*pb.SignalGetMeetingsResp, error) {
	id, err := actor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	cursor, err := s.rooms.Find(ctx, bson.M{"kind": "meeting", "participants.user_id": id, "state": roomActive, "deleted_at": nil, "expires_at": bson.M{"$gt": s.now()}}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := &pb.SignalGetMeetingsResp{MeetingInfoList: []*pb.MeetingInfo{}}
	for cursor.Next(ctx) {
		var room Room
		if err := cursor.Decode(&room); err != nil {
			return nil, err
		}
		meeting, err := room.meeting()
		if err != nil {
			return nil, err
		}
		result.MeetingInfoList = append(result.MeetingInfoList, meeting)
	}
	return result, cursor.Err()
}
func (s *Server) SignalOperateStream(ctx context.Context, req *pb.SignalOperateStreamReq) (*pb.SignalOperateStreamResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	if req.StreamType != "audio" && req.StreamType != "video" && req.StreamType != "screen" {
		return nil, argument("媒体流类型无效")
	}
	_, err = s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, req.RoomID)
		if err != nil {
			return nil, err
		}
		if err := s.requireRoom(tx, room, id); err != nil {
			return nil, err
		}
		if room.Kind != "meeting" {
			return nil, argument("仅会议支持媒体流管理")
		}
		meeting, err := room.meeting()
		if err != nil {
			return nil, err
		}
		target := req.UserID
		if target == "" {
			target = id
		}
		if (req.MuteAll || target != id) && id != room.Host {
			return nil, deny("只有主持人可以管理其他参会者")
		}
		if room.participant(target) == nil {
			return nil, deny("目标用户不在会议中")
		}
		if !req.Mute && id != room.Host {
			if req.StreamType == "audio" && !meeting.ParticipantCanUnmuteSelf || req.StreamType == "video" && !meeting.ParticipantCanEnableVideo {
				return nil, deny("主持人禁止自行开启媒体流")
			}
			if req.StreamType == "screen" && !permission(room, id).Screen {
				return nil, deny("主持人未授予屏幕共享权限")
			}
		}
		targets := []string{target}
		if req.MuteAll {
			targets = nil
			for _, p := range room.Participants {
				if p.UserID != room.Host {
					targets = append(targets, p.UserID)
				}
			}
			if req.StreamType == "audio" {
				meeting.IsMuteAllMicrophone = req.Mute
			}
			if req.StreamType == "video" {
				meeting.IsMuteAllVideo = req.Mute
			}
		}
		for _, user := range targets {
			add, remove := []string{}, []string{}
			if req.Mute {
				add = append(add, user)
			} else {
				remove = append(remove, user)
			}
			if req.StreamType == "audio" {
				meeting.DisableMicrophoneUserIDList = union(meeting.DisableMicrophoneUserIDList, add, remove)
			}
			if req.StreamType == "video" {
				meeting.DisableVideoUserIDList = union(meeting.DisableVideoUserIDList, add, remove)
			}
			if req.StreamType == "screen" && id == room.Host {
				room.participant(user).ScreenDisabled = req.Mute
				if user != room.Host {
					if req.Mute {
						meeting.CanScreenUserIDList = union(meeting.CanScreenUserIDList, nil, []string{user})
					} else {
						meeting.CanScreenUserIDList = union(meeting.CanScreenUserIDList, []string{user}, nil)
					}
				}
			}
		}
		previous := room.Version
		room.Meeting, _ = proto.Marshal(meeting)
		if err := s.save(tx, room, previous); err != nil {
			return nil, err
		}
		if err := s.enqueueMedia(tx, room, "permissions", "", "", false); err != nil {
			return nil, err
		}
		for _, user := range targets {
			if err := s.enqueueMedia(tx, room, "mute", user, req.StreamType, req.Mute); err != nil {
				return nil, err
			}
		}
		if err := s.enqueue(tx, room, id, constant.StreamChangedNotification, &pb.SignalOnStreamChangeReq{RoomID: room.RoomID, StreamType: req.StreamType, Mute: req.Mute}, room.recipients(), nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SignalOperateStreamResp{}, nil
}
func (s *Server) SignalSendCustomSignal(ctx context.Context, req *pb.SignalSendCustomSignalReq) (*pb.SignalSendCustomSignalResp, error) {
	id, err := actor(ctx, "")
	if err != nil {
		return nil, err
	}
	if len(req.CustomInfo) > 8192 {
		return nil, argument("自定义信令过长")
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(req.CustomInfo), &envelope) == nil && (envelope.Type == "roomClosed" || envelope.Type == "meetingInfoUpdated") {
		return nil, deny("该信令类型仅供服务端状态通知")
	}
	_, err = s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		room, err := s.load(tx, req.RoomID)
		if err != nil {
			return nil, err
		}
		if err := s.requireRoom(tx, room, id); err != nil {
			return nil, err
		}
		participant := room.participant(id)
		if participant.Status != "accepted" && participant.Status != "joined" {
			return nil, deny("尚未加入会议")
		}
		previous := room.Version
		if err := s.save(tx, room, previous); err != nil {
			return nil, err
		}
		return nil, s.enqueue(tx, room, id, constant.CustomSignalNotification, req, room.recipients(), nil)
	})
	if err != nil {
		return nil, err
	}
	return &pb.SignalSendCustomSignalResp{}, nil
}
