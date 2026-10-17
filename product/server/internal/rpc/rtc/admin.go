package rtc

import (
	"context"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/pkg/adminauth"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

const adminTokenMetadata = "x-luobo-chart-admin-token"

type chartClaims = adminauth.Claims

func (s *Server) admin(ctx context.Context) error {
	id, err := actor(ctx, "")
	if err != nil {
		return err
	}
	values, _ := ctx.Value(adminTokenMetadata).([]string)
	if len(values) == 0 {
		return authverify.CheckAdmin(ctx)
	}
	if len(values) != 1 {
		return deny("后台认证不可用")
	}
	_, err = adminauth.VerifyChartAdmin(ctx, s.rdb, values[0], id)
	return err
}
func page(pagination *sdkws.RequestPagination) (int64, int64, error) {
	if pagination == nil || pagination.PageNumber < 1 || pagination.PageNumber > 100000 || pagination.ShowNumber < 1 || pagination.ShowNumber > 100 {
		return 0, 0, argument("RTC分页参数无效")
	}
	return int64((pagination.PageNumber - 1) * pagination.ShowNumber), int64(pagination.ShowNumber), nil
}
func timeFilter(filter bson.M, start, end int64) error {
	if start < 0 || end < 0 || end != 0 && start > end {
		return argument("RTC查询时间范围无效")
	}
	if start > 0 || end > 0 {
		window := bson.M{}
		if start > 0 {
			window["$gte"] = time.Unix(start, 0)
		}
		if end > 0 {
			window["$lte"] = time.Unix(end, 0)
		}
		filter["created_at"] = window
	}
	return nil
}
func (s *Server) GetSignalInvitationRecords(ctx context.Context, req *pb.GetSignalInvitationRecordsReq) (*pb.GetSignalInvitationRecordsResp, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	skip, limit, err := page(req.Pagination)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"kind": "call", "deleted_at": nil}
	if req.SendID != "" {
		filter["host"] = req.SendID
	}
	if req.RecvID != "" {
		filter["participants.user_id"] = req.RecvID
	}
	if err := timeFilter(filter, req.StartTime, req.EndTime); err != nil {
		return nil, err
	}
	if req.SessionType != 0 {
		filter["group_id"] = bson.M{"$eq": ""}
		if req.SessionType == constant.GroupChatType || req.SessionType == constant.SuperGroupChatType {
			filter["group_id"] = bson.M{"$ne": ""}
		} else if req.SessionType != constant.SingleChatType {
			return nil, argument("通话会话类型无效")
		}
	}
	total, err := s.rooms.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}
	cursor, err := s.rooms.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip(skip).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := &pb.GetSignalInvitationRecordsResp{Total: int32(total), SignalRecords: []*pb.SignalRecord{}}
	for cursor.Next(ctx) {
		var room Room
		if err := cursor.Decode(&room); err != nil {
			return nil, err
		}
		inv, err := room.invitation()
		if err != nil {
			return nil, err
		}
		record := &pb.SignalRecord{RoomID: room.RoomID, SID: room.RoomID, InviterUserID: room.Host, MediaType: inv.MediaType, SessionType: inv.SessionType, GroupID: room.GroupID, CreateTime: room.CreatedAt.Unix(), InviterUsers: []*pb.SignalUser{}}
		if room.EndedAt != nil {
			record.EndTime = room.EndedAt.Unix()
		}
		for _, p := range room.Participants {
			meta := &pb.ParticipantMetaData{}
			proto.Unmarshal(p.Metadata, meta)
			nickname := ""
			if meta.UserInfo != nil {
				nickname = meta.UserInfo.Nickname
			}
			if p.UserID == room.Host {
				record.InviterUserNickname = nickname
			}
			status := int32(0)
			switch p.Status {
			case "accepted", "joined", "left":
				status = 1
			case "rejected":
				status = 2
			case "busy":
				status = 3
			case "timeout":
				status = 4
			}
			record.InviterUsers = append(record.InviterUsers, &pb.SignalUser{UserID: p.UserID, Nickname: nickname, Status: status})
		}
		result.SignalRecords = append(result.SignalRecords, record)
	}
	return result, cursor.Err()
}
func (s *Server) GetMeetingRecords(ctx context.Context, req *pb.GetMeetingRecordsReq) (*pb.GetMeetingRecordsResp, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	skip, limit, err := page(req.Pagination)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"kind": "meeting", "deleted_at": nil}
	if req.HostUserID != "" {
		filter["host"] = req.HostUserID
	}
	if req.JoinedUserID != "" {
		filter["participants"] = bson.M{"$elemMatch": bson.M{"user_id": req.JoinedUserID, "status": bson.M{"$in": bson.A{"accepted", "joined", "left"}}}}
	}
	if err := timeFilter(filter, req.StartTime, req.EndTime); err != nil {
		return nil, err
	}
	total, err := s.rooms.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}
	cursor, err := s.rooms.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip(skip).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := &pb.GetMeetingRecordsResp{Total: int32(total), MeetingRecords: []*pb.MeetingRecord{}}
	for cursor.Next(ctx) {
		var room Room
		if err := cursor.Decode(&room); err != nil {
			return nil, err
		}
		meeting, err := room.meeting()
		if err != nil {
			return nil, err
		}
		record := &pb.MeetingRecord{RoomID: room.RoomID, MeetingName: meeting.MeetingName, HostUserID: room.Host, Ex: meeting.Ex, CreateTime: meeting.CreateTime, StartTime: meeting.StartTime, EndTime: meeting.EndTime, Status: constant.MeetingNotEnd, JoinedUsers: []*pb.MeetingUser{}, FileRecords: []*pb.FileRecord{}}
		if room.terminal() {
			record.Status = constant.MeetingEnd
		}
		for _, p := range room.Participants {
			if p.Status != "accepted" && p.Status != "joined" && p.Status != "left" {
				continue
			}
			meta := &pb.ParticipantMetaData{}
			proto.Unmarshal(p.Metadata, meta)
			nickname := ""
			if meta.UserInfo != nil {
				nickname = meta.UserInfo.Nickname
			}
			if p.UserID == room.Host {
				record.HostUserNickname = nickname
			}
			record.JoinedUsers = append(record.JoinedUsers, &pb.MeetingUser{UserID: p.UserID, Nickname: nickname})
		}
		result.MeetingRecords = append(result.MeetingRecords, record)
	}
	return result, cursor.Err()
}
func (s *Server) deleteRecords(ctx context.Context, kind string, ids []string) error {
	if err := s.admin(ctx); err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 100 {
		return argument("单次删除RTC记录数量须为1至100")
	}
	for _, id := range ids {
		if !validRoomID(id) {
			return argument("RTC记录ID无效")
		}
	}
	_, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		cursor, err := s.rooms.Find(tx, bson.M{"_id": bson.M{"$in": ids}, "kind": kind, "deleted_at": nil})
		if err != nil {
			return nil, err
		}
		defer cursor.Close(tx)
		rooms := []Room{}
		if err := cursor.All(tx, &rooms); err != nil {
			return nil, err
		}
		for _, room := range rooms {
			if !room.terminal() {
				return nil, argument("请先结束进行中的RTC会话，再删除记录")
			}
		}
		now := s.now()
		_, err = s.rooms.UpdateMany(tx, bson.M{"_id": bson.M{"$in": ids}, "kind": kind, "deleted_at": nil}, bson.M{"$set": bson.M{"deleted_at": now, "deleted_by": mcontext.GetOpUserID(ctx)}})
		return nil, err
	})
	return err
}
func (s *Server) DeleteSignalRecords(ctx context.Context, req *pb.DeleteSignalRecordsReq) (*pb.DeleteSignalRecordsResp, error) {
	if err := s.deleteRecords(ctx, "call", req.SIDs); err != nil {
		return nil, err
	}
	return &pb.DeleteSignalRecordsResp{}, nil
}
func (s *Server) DeleteMeetingRecords(ctx context.Context, req *pb.DeleteMeetingRecordsReq) (*pb.DeleteMeetingRecordsResp, error) {
	if err := s.deleteRecords(ctx, "meeting", req.RoomIDs); err != nil {
		return nil, err
	}
	return &pb.DeleteMeetingRecordsResp{}, nil
}
