package rtc

import (
	"context"
	"encoding/json"
	"time"

	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"google.golang.org/protobuf/proto"
)

const (
	roomRinging   = "ringing"
	roomActive    = "active"
	roomEnded     = "ended"
	roomCancelled = "cancelled"
	roomTimeout   = "timeout"
)

type Participant struct {
	UserID         string    `bson:"user_id"`
	Status         string    `bson:"status"`
	PlatformID     int32     `bson:"platform_id"`
	Metadata       []byte    `bson:"metadata"`
	UpdatedAt      time.Time `bson:"updated_at"`
	MediaSID       string    `bson:"media_sid"`
	MediaJoinedAt  int64     `bson:"media_joined_at"`
	ScreenDisabled bool      `bson:"screen_disabled"`
}
type Room struct {
	RoomID       string        `bson:"_id"`
	Kind         string        `bson:"kind"`
	Host         string        `bson:"host"`
	GroupID      string        `bson:"group_id"`
	State        string        `bson:"state"`
	Version      int64         `bson:"version"`
	Invitation   []byte        `bson:"invitation"`
	Meeting      []byte        `bson:"meeting"`
	OfflinePush  []byte        `bson:"offline_push"`
	Participants []Participant `bson:"participants"`
	CreatedAt    time.Time     `bson:"created_at"`
	ExpiresAt    time.Time     `bson:"expires_at"`
	EndedAt      *time.Time    `bson:"ended_at"`
	DeletedAt    *time.Time    `bson:"deleted_at"`
}

func (r *Room) invitation() (*pb.InvitationInfo, error) {
	value := &pb.InvitationInfo{}
	if err := proto.Unmarshal(r.Invitation, value); err != nil {
		return nil, err
	}
	return value, nil
}
func (r *Room) meeting() (*pb.MeetingInfo, error) {
	value := &pb.MeetingInfo{}
	if err := proto.Unmarshal(r.Meeting, value); err != nil {
		return nil, err
	}
	return value, nil
}
func (r *Room) terminal() bool { return r.State != roomRinging && r.State != roomActive }
func (r *Room) participant(actor string) *Participant {
	for i := range r.Participants {
		if r.Participants[i].UserID == actor {
			return &r.Participants[i]
		}
	}
	return nil
}
func (r *Room) recipients() []string {
	out := []string{}
	for _, p := range r.Participants {
		if p.Status != "busy" {
			out = append(out, p.UserID)
		}
	}
	return out
}
func (r *Room) metadata() []*pb.ParticipantMetaData {
	out := []*pb.ParticipantMetaData{}
	for _, p := range r.Participants {
		if p.Status != "accepted" && p.Status != "joined" {
			continue
		}
		meta := &pb.ParticipantMetaData{}
		if proto.Unmarshal(p.Metadata, meta) == nil {
			out = append(out, meta)
		}
	}
	return out
}

type Outbox struct {
	ID            string     `bson:"_id"`
	RoomID        string     `bson:"room_id"`
	Revision      int64      `bson:"revision"`
	Sender        string     `bson:"sender"`
	Receiver      string     `bson:"receiver"`
	ContentType   int32      `bson:"content_type"`
	Payload       []byte     `bson:"payload"`
	OfflinePush   []byte     `bson:"offline_push"`
	MediaAction   string     `bson:"media_action"`
	Target        string     `bson:"target"`
	Stream        string     `bson:"stream"`
	Mute          bool       `bson:"mute"`
	CreatedAt     time.Time  `bson:"created_at"`
	NextAttemptAt time.Time  `bson:"next_attempt_at"`
	Attempts      int        `bson:"attempts"`
	LeaseToken    string     `bson:"lease_token"`
	LeaseUntil    time.Time  `bson:"lease_until"`
	DeliveredAt   *time.Time `bson:"delivered_at"`
	ExpireAt      *time.Time `bson:"expire_at"`
}
type Access interface {
	Metadata(context.Context, string, string) (*pb.ParticipantMetaData, error)
	Invitees(context.Context, string, string, []string) error
}
type Delivery interface {
	Send(context.Context, *Outbox) error
}
type Server struct {
	rdb redis.UniversalClient
	pb.UnimplementedRtcServiceServer
	db                    *mongo.Database
	rooms, outbox, active *mongo.Collection
	access                Access
	media                 Media
	delivery              Delivery
	now                   func() time.Time
}

func New(ctx context.Context, db *mongo.Database, access Access, media Media, delivery Delivery) (*Server, error) {
	var topology struct {
		SetName string `bson:"setName"`
		Message string `bson:"msg"`
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&topology); err != nil {
		return nil, err
	}
	if topology.SetName == "" && topology.Message != "isdbgrid" {
		return nil, argument("RTC需要MongoDB副本集或mongos以提供持久事务")
	}
	concern := options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true)))
	server := &Server{db: db, rooms: db.Collection("rtc_rooms", concern), outbox: db.Collection("rtc_outbox", concern), active: db.Collection("rtc_active_users", concern), access: access, media: media, delivery: delivery, now: func() time.Time { return time.Now().UTC() }}
	_, err := server.rooms.Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "participants.user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "created_at", Value: -1}}}, {Keys: bson.D{{Key: "group_id", Value: 1}, {Key: "state", Value: 1}}}, {Keys: bson.D{{Key: "host", Value: 1}, {Key: "created_at", Value: -1}}}, {Keys: bson.D{{Key: "state", Value: 1}, {Key: "expires_at", Value: 1}}}})
	if err != nil {
		return nil, err
	}
	_, err = server.outbox.Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "receiver", Value: 1}, {Key: "delivered_at", Value: 1}, {Key: "revision", Value: 1}, {Key: "created_at", Value: 1}}}, {Keys: bson.D{{Key: "delivered_at", Value: 1}, {Key: "next_attempt_at", Value: 1}, {Key: "lease_until", Value: 1}}}, {Keys: bson.D{{Key: "expire_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}})
	if err != nil {
		return nil, err
	}
	_, err = db.Collection("rtc_delivery_lanes").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "expire_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)})
	if err != nil {
		return nil, err
	}
	return server, nil
}
func actor(ctx context.Context, claimed string) (string, error) {
	id := mcontext.GetOpUserID(ctx)
	if id == "" || len(id) > 64 || claimed != "" && claimed != id {
		return "", errs.ErrNoPermission.Wrap("RTC操作身份不匹配")
	}
	return id, nil
}
func validRoomID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func uniqueUsers(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	if len(ids) > 100 {
		return nil, errs.ErrArgs.Wrap("单次最多邀请100人")
	}
	for _, id := range ids {
		if id == "" || len(id) > 64 || seen[id] {
			return nil, errs.ErrArgs.Wrap("邀请用户列表无效或重复")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}
func (s *Server) transaction(ctx context.Context, fn func(mongo.SessionContext) (interface{}, error)) (interface{}, error) {
	session, err := s.db.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	return session.WithTransaction(ctx, fn, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
}
func (s *Server) load(ctx context.Context, id string) (*Room, error) {
	if !validRoomID(id) {
		return nil, errs.ErrArgs.Wrap("RTC房间ID无效")
	}
	var room Room
	err := s.rooms.FindOne(ctx, bson.M{"_id": id, "deleted_at": nil}).Decode(&room)
	if err == mongo.ErrNoDocuments {
		return nil, errs.ErrRecordNotFound.Wrap("RTC房间不存在")
	}
	return &room, err
}
func (s *Server) save(ctx context.Context, room *Room, previous int64) error {
	room.Version = previous + 1
	result, err := s.rooms.ReplaceOne(ctx, bson.M{"_id": room.RoomID, "version": previous}, room)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return errs.ErrData.Wrap("RTC状态已变更，请重试")
	}
	return nil
}
func (s *Server) requireRoom(ctx context.Context, room *Room, id string) error {
	if room.terminal() || !room.ExpiresAt.After(s.now()) {
		return errs.ErrData.Wrap("RTC房间已结束")
	}
	participant := room.participant(id)
	if participant == nil || participant.Status == "busy" || participant.Status == "rejected" || participant.Status == "left" {
		return errs.ErrNoPermission.Wrap("无权加入此RTC房间")
	}
	if room.GroupID != "" {
		if _, err := s.access.Metadata(ctx, id, room.GroupID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) certificate(room *Room, id string) (string, error) {
	meta := room.participant(id)
	if meta == nil {
		return "", errs.ErrNoPermission.Wrap("RTC参与者不存在")
	}
	value := &pb.ParticipantMetaData{}
	if err := proto.Unmarshal(meta.Metadata, value); err != nil {
		return "", err
	}
	metadata, err := jsonMetadata(value)
	if err != nil {
		return "", err
	}
	return s.media.Token(room.RoomID, id, metadata, permission(room, id))
}
func jsonMetadata(value *pb.ParticipantMetaData) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
func permission(room *Room, id string) PublishPermission {
	result := PublishPermission{Microphone: true, Camera: true, Screen: true}
	if room.Kind != "meeting" {
		inv, _ := room.invitation()
		result.Camera = inv != nil && inv.MediaType == "video"
		result.Screen = false
		return result
	}
	meeting, err := room.meeting()
	if err != nil {
		return PublishPermission{}
	}
	host := meeting.HostUserID == id
	contains := func(values []string) bool {
		for _, value := range values {
			if value == id {
				return true
			}
		}
		return false
	}
	result.Microphone = host || !contains(meeting.DisableMicrophoneUserIDList)
	result.Camera = host || !contains(meeting.DisableVideoUserIDList)
	result.Screen = host || !meeting.OnlyHostShareScreen || contains(meeting.CanScreenUserIDList)
	if participant := room.participant(id); participant != nil && participant.ScreenDisabled {
		result.Screen = false
	}
	return result
}
func (s *Server) end(ctx context.Context, room *Room, state string) error {
	now := s.now()
	room.State = state
	room.EndedAt = &now
	if _, err := s.active.DeleteMany(ctx, bson.M{"room_id": room.RoomID}); err != nil {
		return err
	}
	return nil
}
func deny(message string) error     { return errs.ErrNoPermission.Wrap(message) }
func argument(message string) error { return errs.ErrArgs.Wrap(message) }

func isMissing(err error) bool {
	if err == nil {
		return false
	}
	code, ok := errs.Unwrap(err).(interface{ Code() int })
	return ok && code.Code() == errs.ErrRecordNotFound.Code()
}
