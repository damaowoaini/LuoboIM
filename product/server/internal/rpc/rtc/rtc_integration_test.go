package rtc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

type accessFixture struct{}

func (accessFixture) Metadata(_ context.Context, id, group string) (*pb.ParticipantMetaData, error) {
	if id == "outsider" && group != "" {
		return nil, deny("not a member")
	}
	return &pb.ParticipantMetaData{UserInfo: &sdkws.PublicUserInfo{UserID: id, Nickname: "User " + id}, GroupInfo: &sdkws.GroupInfo{GroupID: group}}, nil
}
func (accessFixture) Invitees(_ context.Context, _ string, group string, users []string) error {
	for _, id := range users {
		if id == "missing" || id == "outsider" && group != "" {
			return deny("invalid invitee")
		}
	}
	return nil
}

type mediaFixture struct {
	mutex            sync.Mutex
	deleted, removed int
	fail             bool
}

func (m *mediaFixture) Token(room, id, metadata string, permission PublishPermission) (string, error) {
	return "fixture-token", nil
}
func (m *mediaFixture) URL() string { return "wss://rtc.test.invalid" }
func (m *mediaFixture) DeleteRoom(context.Context, string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.deleted++
	if m.fail {
		return errors.New("temporary media outage")
	}
	return nil
}
func (m *mediaFixture) Remove(context.Context, string, string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.removed++
	return nil
}
func (m *mediaFixture) Permissions(context.Context, string, string, PublishPermission) error {
	return nil
}
func (m *mediaFixture) Mute(context.Context, string, string, string, bool) error { return nil }
func (m *mediaFixture) VerifyWebhook([]byte, string) error                       { return nil }

type deliveryFixture struct {
	mutex  sync.Mutex
	fail   bool
	events []Outbox
}

func (d *deliveryFixture) Send(_ context.Context, event *Outbox) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.fail {
		return errors.New("gateway unavailable")
	}
	d.events = append(d.events, *event)
	return nil
}
func rtcContext(user string, platform int) context.Context {
	ctx := context.WithValue(context.Background(), constant.OpUserID, user)
	return context.WithValue(ctx, constant.OpUserPlatform, constant.PlatformIDToName(platform))
}
func rtcMongo(t *testing.T) (*Server, *mongo.Database, *mediaFixture, *deliveryFixture) {
	t.Helper()
	uri := os.Getenv("LUOBO_INTEGRATION_MONGO_URI")
	if uri == "" {
		t.Skip("requires task-private Mongo replica set")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") {
		t.Fatal("only task-private Mongo endpoint permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal("cannot connect to private Mongo")
	}
	db := client.Database("luobo_rtc_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Error("test database cleanup failed")
		}
		client.Disconnect(ctx)
	})
	media, delivery := &mediaFixture{}, &deliveryFixture{}
	server, err := New(ctx, db, accessFixture{}, media, delivery)
	if err != nil {
		t.Fatal(err)
	}
	return server, db, media, delivery
}
func inviteRequest(room, inviter, invitee string) *pb.SignalMessageAssembleReq {
	return &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_Invite{Invite: &pb.SignalInviteReq{UserID: inviter, Invitation: &pb.InvitationInfo{RoomID: room, InviterUserID: inviter, InviteeUserIDList: []string{invitee}, SessionType: constant.SingleChatType, MediaType: "audio", Timeout: 30}}}}}
}
func acceptRequest(inv *pb.InvitationInfo, user string) *pb.SignalMessageAssembleReq {
	return &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_Accept{Accept: &pb.SignalAcceptReq{Invitation: inv, UserID: user}}}}
}
func TestMongoRTCCallOwnershipIdempotencyAndCompetingDevices(t *testing.T) {
	s, db, _, _ := rtcMongo(t)
	caller := rtcContext("u1", 2)
	roomID := uuid.NewString()
	req := inviteRequest(roomID, "u1", "u2")
	if _, err := s.SignalMessageAssemble(rtcContext("attacker", 2), req); err == nil {
		t.Fatal("forged inviter accepted")
	}
	first, err := s.SignalMessageAssemble(caller, req)
	if err != nil || first.SignalResp.GetInvite().RoomID != roomID {
		t.Fatal("invite failed", err)
	}
	if _, err := s.SignalMessageAssemble(caller, req); err != nil {
		t.Fatal("same intent retry not idempotent", err)
	}
	if count, _ := db.Collection("rtc_outbox").CountDocuments(context.Background(), bson.M{"room_id": roomID}); count != 1 {
		t.Fatal("duplicate invite emitted multiple outbox events")
	}
	restored, err := s.GetSignalInvitationInfoStartApp(rtcContext("u2", 1), &pb.GetSignalInvitationInfoStartAppReq{UserID: "u2"})
	if err != nil || restored.Invitation == nil || restored.Invitation.RoomID != roomID {
		t.Fatal("offline invite recovery failed", err)
	}
	if _, err := s.GetSignalInvitationInfo(rtcContext("attacker", 1), &pb.GetSignalInvitationInfoReq{RoomID: roomID}); err == nil {
		t.Fatal("outsider queried invitation")
	}
	if _, err := s.SignalGetTokenByRoomID(rtcContext("u2", 1), &pb.SignalGetTokenByRoomIDReq{RoomID: roomID, UserID: "u2"}); err == nil {
		t.Fatal("token lookup bypassed accept")
	}
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, device := range []int{1, 2} {
		wg.Add(1)
		go func(device int) {
			defer wg.Done()
			<-start
			if _, err := s.SignalMessageAssemble(rtcContext("u2", device), acceptRequest(restored.Invitation, "u2")); err == nil {
				results <- device
			}
		}(device)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := 0
	count := 0
	for platform := range results {
		winner = platform
		count++
	}
	if count != 1 {
		t.Fatalf("expected one winning device, got %d", count)
	}
	if _, err := s.SignalMessageAssemble(rtcContext("u2", winner), acceptRequest(restored.Invitation, "u2")); err != nil {
		t.Fatal("winning accept retry failed", err)
	}
	cancel := &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_Cancel{Cancel: &pb.SignalCancelReq{UserID: "u1", Invitation: restored.Invitation}}}}
	if _, err := s.SignalMessageAssemble(caller, cancel); err == nil {
		t.Fatal("late cancel changed accepted state")
	}
	if _, err := s.SignalMessageAssemble(rtcContext("attacker", 1), acceptRequest(restored.Invitation, "attacker")); err == nil {
		t.Fatal("outsider accepted call")
	}
	busy, err := s.SignalMessageAssemble(rtcContext("u3", 2), inviteRequest(uuid.NewString(), "u3", "u2"))
	if err != nil || len(busy.SignalResp.GetInvite().BusyLineUserIDList) != 1 {
		t.Fatal("active callee not marked busy", err)
	}
	hung := &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_HungUp{HungUp: &pb.SignalHungUpReq{UserID: "u2", Invitation: restored.Invitation}}}}
	if _, err := s.SignalMessageAssemble(rtcContext("u2", winner), hung); err != nil {
		t.Fatal("hangup failed", err)
	}
	if _, err := s.SignalMessageAssemble(rtcContext("u2", winner), hung); err != nil {
		t.Fatal("hangup retry failed", err)
	}
	if _, err := s.SignalGetTokenByRoomID(caller, &pb.SignalGetTokenByRoomIDReq{RoomID: roomID}); err == nil {
		t.Fatal("ended call produced new media token")
	}
	if leases, _ := s.active.CountDocuments(context.Background(), bson.M{"room_id": roomID}); leases != 0 {
		t.Fatal("terminal call retained busy leases")
	}
}
func TestMongoRTCOutboxSurvivesRestartAndSkipsStaleInvitation(t *testing.T) {
	s, db, media, delivery := rtcMongo(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	id := uuid.NewString()
	ctx := rtcContext("u1", 2)
	req := inviteRequest(id, "u1", "u2")
	if _, err := s.SignalMessageAssemble(ctx, req); err != nil {
		t.Fatal(err)
	}
	delivery.fail = true
	if err := s.ProcessOutbox(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	var pending Outbox
	if err := s.outbox.FindOne(context.Background(), bson.M{"room_id": id, "receiver": "u2"}).Decode(&pending); err != nil || pending.DeliveredAt != nil || pending.Attempts != 1 {
		t.Fatal("failed delivery not durable")
	}
	restarted, err := New(context.Background(), db, accessFixture{}, media, delivery)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return now }
	room, err := restarted.load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	inv, _ := room.invitation()
	if _, err := restarted.SignalMessageAssemble(ctx, &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_Cancel{Cancel: &pb.SignalCancelReq{UserID: "u1", Invitation: inv}}}}); err != nil {
		t.Fatal(err)
	}
	delivery.fail = false
	now = now.Add(3 * time.Second)
	for i := 0; i < 3; i++ {
		if err := restarted.ProcessOutbox(context.Background(), 100); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range delivery.events {
		request := &pb.SignalReq{}
		proto.Unmarshal(event.Payload, request)
		if request.GetInvite() != nil {
			t.Fatal("cancelled invitation delivered after worker restart")
		}
	}
	if len(delivery.events) == 0 || media.deleted == 0 {
		t.Fatal("persisted cancellation/media cleanup was not reconciled")
	}
	if count, _ := s.outbox.CountDocuments(context.Background(), bson.M{"delivered_at": nil}); count != 0 {
		t.Fatal("outbox jobs were abandoned")
	}
}
func TestMongoRTCMeetingContractsAndHostBoundaries(t *testing.T) {
	s, _, _, _ := rtcMongo(t)
	host, guest := rtcContext("host", 2), rtcContext("guest", 1)
	created, err := s.SignalCreateMeeting(host, &pb.SignalCreateMeetingReq{MeetingName: "Review", InviteeUserIDList: []string{"guest"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalJoinMeeting(rtcContext("outsider", 1), &pb.SignalJoinMeetingReq{RoomID: created.RoomID}); err == nil {
		t.Fatal("outsider joined uninvited meeting")
	}
	if _, err := s.SignalJoinMeeting(guest, &pb.SignalJoinMeetingReq{RoomID: created.RoomID, UserID: "guest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalUpdateMeetingInfo(guest, &pb.SignalUpdateMeetingInfoReq{RoomID: created.RoomID, MeetingName: wrapperspb.String("stolen")}); err == nil {
		t.Fatal("guest changed host metadata")
	}
	if _, err := s.SignalUpdateMeetingInfo(host, &pb.SignalUpdateMeetingInfoReq{RoomID: created.RoomID, MeetingName: wrapperspb.String("Renamed"), OnlyHostShareScreen: wrapperspb.Bool(true), ParticipantCanUnmuteSelf: wrapperspb.Bool(false)}); err != nil {
		t.Fatal(err)
	}
	meetings, err := s.SignalGetMeetings(guest, &pb.SignalGetMeetingsReq{UserID: "guest"})
	if err != nil || len(meetings.MeetingInfoList) != 1 || meetings.MeetingInfoList[0].MeetingName != "Renamed" {
		t.Fatal("meeting list not durable", err)
	}
	if _, err := s.SignalOperateStream(guest, &pb.SignalOperateStreamReq{RoomID: created.RoomID, UserID: "host", StreamType: "audio", Mute: true}); err == nil {
		t.Fatal("guest muted host")
	}
	if _, err := s.SignalOperateStream(guest, &pb.SignalOperateStreamReq{RoomID: created.RoomID, UserID: "guest", StreamType: "screen", Mute: false}); err == nil {
		t.Fatal("guest shared screen without grant")
	}
	if _, err := s.SignalOperateStream(host, &pb.SignalOperateStreamReq{RoomID: created.RoomID, StreamType: "audio", MuteAll: true, Mute: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalOperateStream(guest, &pb.SignalOperateStreamReq{RoomID: created.RoomID, StreamType: "audio", Mute: false}); err == nil {
		t.Fatal("guest bypassed host unmute restriction")
	}
	if _, err := s.SignalSendCustomSignal(guest, &pb.SignalSendCustomSignalReq{RoomID: created.RoomID, CustomInfo: `{"type":"roomClosed"}`}); err == nil {
		t.Fatal("participant forged server room-closed signal")
	}
	if _, err := s.SignalSendCustomSignal(guest, &pb.SignalSendCustomSignalReq{RoomID: created.RoomID, CustomInfo: `{"hello":true}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalCloseRoom(guest, &pb.SignalCloseRoomReq{RoomID: created.RoomID}); err == nil {
		t.Fatal("guest closed host room")
	}
	if _, err := s.SignalCloseRoom(host, &pb.SignalCloseRoomReq{RoomID: created.RoomID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalJoinMeeting(guest, &pb.SignalJoinMeetingReq{RoomID: created.RoomID}); err == nil {
		t.Fatal("closed room issued a new token")
	}
	old := config.Config.Manager.UserID
	config.Config.Manager.UserID = []string{"admin"}
	defer func() { config.Config.Manager.UserID = old }()
	admin := rtcContext("admin", 5)
	if _, err := s.GetMeetingRecords(guest, &pb.GetMeetingRecordsReq{Pagination: &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20}}); err == nil {
		t.Fatal("guest read admin records")
	}
	records, err := s.GetMeetingRecords(admin, &pb.GetMeetingRecordsReq{Pagination: &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20}})
	if err != nil || records.Total != 1 || records.MeetingRecords[0].Status != constant.MeetingEnd {
		t.Fatal("admin meeting records incompatible", err)
	}
	if _, err := s.DeleteMeetingRecords(admin, &pb.DeleteMeetingRecordsReq{RoomIDs: []string{created.RoomID}}); err != nil {
		t.Fatal(err)
	}
	records, err = s.GetMeetingRecords(admin, &pb.GetMeetingRecordsReq{Pagination: &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20}})
	if err != nil || records.Total != 0 {
		t.Fatal("record deletion not reflected", err)
	}
}
func TestMongoRTCWebhookReplayAndTimeoutAreDurable(t *testing.T) {
	s, _, _, _ := rtcMongo(t)
	ctx := rtcContext("u1", 2)
	roomID := uuid.NewString()
	if _, err := s.SignalMessageAssemble(ctx, inviteRequest(roomID, "u1", "u2")); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "event": "participant_joined", "room": map[string]string{"name": roomID}, "participant": map[string]string{"identity": "u1"}})
	if err := s.HandleWebhook(context.Background(), body, "verified-fixture"); err != nil {
		t.Fatal(err)
	}
	first, _ := s.load(ctx, roomID)
	if err := s.HandleWebhook(context.Background(), body, "verified-fixture"); err != nil {
		t.Fatal(err)
	}
	second, _ := s.load(ctx, roomID)
	if first.Version != second.Version {
		t.Fatal("provider webhook replay changed revision twice")
	}
	s.now = func() time.Time { return time.Now().UTC().Add(time.Minute) }
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	ended, err := s.load(ctx, roomID)
	if err != nil || ended.State != roomTimeout {
		t.Fatal("timeout did not persist terminal state", err)
	}
	if _, err := s.SignalMessageAssemble(rtcContext("u2", 1), acceptRequest(func() *pb.InvitationInfo { inv, _ := ended.invitation(); return inv }(), "u2")); err == nil {
		t.Fatal("late acceptance resurrected expired call")
	}
}

func TestMongoRTCAtomicRoomAndOutboxRollback(t *testing.T) {
	s, db, _, _ := rtcMongo(t)
	ctx := rtcContext("host", 2)
	roomID := uuid.NewString()
	// Inject a deterministic failure on the second recipient write. The real
	// Mongo transaction must roll back the room, first event, and busy leases.
	_, err := db.Collection("rtc_outbox").Indexes().CreateOne(context.Background(), mongo.IndexModel{Keys: bson.D{{Key: "content_type", Value: 1}}, Options: options.Index().SetUnique(true).SetName("fixture_failure")})
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_InviteInGroup{InviteInGroup: &pb.SignalInviteInGroupReq{UserID: "host", Invitation: &pb.InvitationInfo{RoomID: roomID, InviterUserID: "host", GroupID: "group", InviteeUserIDList: []string{"a", "b"}, SessionType: constant.SuperGroupChatType, MediaType: "video", Timeout: 30}}}}}
	if _, err := s.SignalMessageAssemble(ctx, req); err == nil {
		t.Fatal("injected event failure unexpectedly succeeded")
	}
	for _, collection := range []string{"rtc_rooms", "rtc_outbox", "rtc_active_users"} {
		if count, _ := db.Collection(collection).CountDocuments(context.Background(), bson.M{}); count != 0 {
			t.Fatal("partial RTC transaction persisted", collection)
		}
	}
	if _, err := db.Collection("rtc_outbox").Indexes().DropOne(context.Background(), "fixture_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalMessageAssemble(ctx, req); err != nil {
		t.Fatal("retry after rollback failed", err)
	}
	if _, err := s.SignalGetRoomByGroupID(rtcContext("outsider", 1), &pb.SignalGetRoomByGroupIDReq{GroupID: "group"}); err == nil {
		t.Fatal("nonmember discovered private group room")
	}
	found, err := s.SignalGetRoomByGroupID(rtcContext("a", 1), &pb.SignalGetRoomByGroupIDReq{GroupID: "group"})
	if err != nil || found.RoomID != roomID {
		t.Fatal("group room lookup failed", err)
	}
	if _, err := s.SignalGetTokenByRoomID(rtcContext("outsider", 1), &pb.SignalGetTokenByRoomIDReq{RoomID: roomID}); err == nil {
		t.Fatal("nonmember joined private group room")
	}
	if _, err := s.SignalGetTokenByRoomID(rtcContext("new-current-member", 1), &pb.SignalGetTokenByRoomIDReq{RoomID: roomID}); err != nil {
		t.Fatal("current group member could not join", err)
	}
	if _, err := s.SignalGetRooms(rtcContext("outsider", 1), &pb.SignalGetRoomsReq{RoomIDs: []string{roomID}}); err == nil {
		t.Fatal("outsider queried room details")
	}
	if _, err := s.GetSignalInvitationInfo(rtcContext("a", 1), &pb.GetSignalInvitationInfoReq{RoomID: roomID}); err != nil {
		t.Fatal(err)
	}
}
func TestMongoRTCConcurrentInvitationAndCancelAccept(t *testing.T) {
	s, db, _, _ := rtcMongo(t)
	ctx := rtcContext("u1", 2)
	roomID := uuid.NewString()
	req := inviteRequest(roomID, "u1", "u2")
	errors := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.SignalMessageAssemble(ctx, req); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("same invitation retry was not idempotent", err)
		}
	}
	if count, _ := db.Collection("rtc_rooms").CountDocuments(context.Background(), bson.M{}); count != 1 {
		t.Fatal("duplicate invitation rooms")
	}
	if count, _ := db.Collection("rtc_outbox").CountDocuments(context.Background(), bson.M{}); count != 1 {
		t.Fatal("duplicate invitation events")
	}
	room, _ := s.load(ctx, roomID)
	inv, _ := room.invitation()
	start := make(chan struct{})
	outcomes := make(chan struct {
		action string
		err    error
	}, 2)
	go func() {
		<-start
		_, err := s.SignalMessageAssemble(rtcContext("u2", 1), acceptRequest(inv, "u2"))
		outcomes <- struct {
			action string
			err    error
		}{"accept", err}
	}()
	go func() {
		<-start
		_, err := s.SignalMessageAssemble(ctx, &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_Cancel{Cancel: &pb.SignalCancelReq{Invitation: inv, UserID: "u1"}}}})
		outcomes <- struct {
			action string
			err    error
		}{"cancel", err}
	}()
	close(start)
	success := 0
	var winner string
	for i := 0; i < 2; i++ {
		result := <-outcomes
		if result.err == nil {
			success++
			winner = result.action
		}
	}
	if success != 1 {
		t.Fatal("cancel and accept did not have exactly one winner")
	}
	saved, _ := s.load(ctx, roomID)
	if winner == "accept" && saved.State != roomActive || winner == "cancel" && saved.State != roomCancelled {
		t.Fatal("winning action and persisted state diverged")
	}
}
func TestMongoRTCMeetingUnmuteScreenAndStaleMediaSession(t *testing.T) {
	s, _, _, _ := rtcMongo(t)
	host, guest := rtcContext("host", 2), rtcContext("guest", 1)
	created, err := s.SignalCreateMeeting(host, &pb.SignalCreateMeetingReq{MeetingName: "Permissions", InviteeUserIDList: []string{"guest"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalJoinMeeting(guest, &pb.SignalJoinMeetingReq{RoomID: created.RoomID}); err != nil {
		t.Fatal(err)
	}
	room, _ := s.load(host, created.RoomID)
	if permission(room, "guest").Microphone {
		t.Fatal("join-disable-microphone ignored")
	}
	if _, err := s.SignalOperateStream(host, &pb.SignalOperateStreamReq{RoomID: created.RoomID, StreamType: "audio", MuteAll: true, Mute: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignalOperateStream(guest, &pb.SignalOperateStreamReq{RoomID: created.RoomID, StreamType: "audio", Mute: false}); err != nil {
		t.Fatal(err)
	}
	room, _ = s.load(host, created.RoomID)
	if !permission(room, "guest").Microphone {
		t.Fatal("permitted self-unmute did not change media grants")
	}
	if _, err := s.SignalOperateStream(host, &pb.SignalOperateStreamReq{RoomID: created.RoomID, UserID: "guest", StreamType: "screen", Mute: false}); err != nil {
		t.Fatal(err)
	}
	room, _ = s.load(host, created.RoomID)
	if !permission(room, "guest").Screen {
		t.Fatal("host screen grant not effective")
	}
	if _, err := s.SignalOperateStream(host, &pb.SignalOperateStreamReq{RoomID: created.RoomID, UserID: "guest", StreamType: "screen", Mute: true}); err != nil {
		t.Fatal(err)
	}
	room, _ = s.load(host, created.RoomID)
	if permission(room, "guest").Screen {
		t.Fatal("host screen revoke not effective")
	}
	if _, err := s.SignalOperateStream(guest, &pb.SignalOperateStreamReq{RoomID: created.RoomID, StreamType: "screen", Mute: false}); err == nil {
		t.Fatal("guest restored host-revoked screen grant")
	}
	event := func(kind, sid string, createdAt int64) {
		body, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "event": kind, "createdAt": createdAt, "room": map[string]string{"name": created.RoomID}, "participant": map[string]string{"identity": "guest", "sid": sid}})
		if err := s.HandleWebhook(context.Background(), body, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	event("participant_joined", "PA_old", 10)
	event("participant_joined", "PA_new", 20)
	event("participant_left", "PA_old", 30)
	room, _ = s.load(host, created.RoomID)
	if room.participant("guest").Status != "joined" || room.participant("guest").MediaSID != "PA_new" {
		t.Fatal("delayed old-media disconnect evicted current session")
	}
	event("participant_left", "PA_new", 31)
	room, _ = s.load(host, created.RoomID)
	if room.participant("guest").Status != "left" {
		t.Fatal("current session disconnect ignored")
	}
}
