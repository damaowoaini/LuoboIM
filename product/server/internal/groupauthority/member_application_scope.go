package groupauthority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type MemberApplicationKind string

const (
	MemberApplicationCreated  MemberApplicationKind = "application-created"
	MemberApplicationAccepted MemberApplicationKind = "application-accepted"
	MemberApplicationRejected MemberApplicationKind = "application-rejected"
)

type memberApplicationKey struct{}
type memberApplicationCommand struct {
	info       NoticeCommandInfo
	kind       MemberApplicationKind
	applicants []string
}

func NewMemberApplicationCommand(ctx context.Context, epoch, gid string, ids []string, kind MemberApplicationKind) (context.Context, NoticeCommandInfo, error) {
	if ctx == nil || !canonicalUUID(epoch) || !noticeIdentity(gid) || !noticeIdentity(mcontext.GetOpUserID(ctx)) || len(ids) == 0 || ctx.Value(memberApplicationKey{}) != nil || ctx.Value(memberAdmissionKey{}) != nil {
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	if kind != MemberApplicationCreated && kind != MemberApplicationAccepted && kind != MemberApplicationRejected {
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	for i, uid := range ordered {
		if !noticeIdentity(uid) || i > 0 && uid == ordered[i-1] {
			return nil, NoticeCommandInfo{}, ErrInvalidScope
		}
	}
	if kind != MemberApplicationCreated && len(ordered) != 1 {
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, NoticeCommandInfo{}, ErrUnavailable
	}
	action := NoticeAction(kind)
	if kind == MemberApplicationAccepted {
		action = NoticeAction(MemberAdmissionApprove)
	}
	c := &memberApplicationCommand{info: NoticeCommandInfo{ID: id.String(), Epoch: epoch, Actor: mcontext.GetOpUserID(ctx), GroupID: gid, Action: action, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}, kind: kind, applicants: ordered}
	return context.WithValue(ctx, memberApplicationKey{}, c), c.info, nil
}

// Admission and the accepted direct notices are one server command. Only this
// private original application intent may supply the otherwise fresh ID.
func admissionApplicationCommand(ctx context.Context, epoch, gid string, ids []string, kind MemberAdmissionKind) (*memberAdmissionCommand, error) {
	value := ctx.Value(memberApplicationKey{})
	if value == nil {
		return nil, nil
	}
	c, ok := value.(*memberApplicationCommand)
	if !ok || c == nil || c.kind != MemberApplicationAccepted || kind != MemberAdmissionApprove || c.info.Epoch != epoch || c.info.GroupID != gid || c.info.Actor != mcontext.GetOpUserID(ctx) || !reflect.DeepEqual(c.applicants, ids) {
		return nil, ErrInvalidScope
	}
	return &memberAdmissionCommand{info: c.info, members: append([]string(nil), ids...)}, nil
}

type MemberApplicationScope struct {
	scope                   *mutationScope
	session                 mongo.Session
	command                 *memberApplicationCommand
	preGroup, postGroup     relation.GroupModel
	preMembers, postMembers map[string]relation.GroupMemberModel
	pre, post               map[string]relation.GroupRequestModel
	profiles                map[string]*sdkws.PublicUserInfo
	revision                int64
	finished                bool
	messages                map[string]MemberApplicationMessageInfo
}
type MemberApplicationInfo struct {
	Command                NoticeCommandInfo
	Kind                   MemberApplicationKind
	GroupRevision          int64
	Applicants, Recipients []string
}
type MemberApplicationMessageInfo struct {
	Command                             NoticeCommandInfo
	Kind                                MemberApplicationKind
	Applicant, Recipient, MessageSHA256 string
}

func readMemberApplications(ctx context.Context, db *mongo.Database, gid string, ids []string) (map[string]relation.GroupRequestModel, error) {
	result := map[string]relation.GroupRequestModel{}
	cur, err := db.Collection("group_request").Find(ctx, bson.M{"group_id": gid, "user_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	for cur.Next(ctx) {
		var row relation.GroupRequestModel
		if err = cur.Decode(&row); err != nil {
			break
		}
		if row.GroupID != gid || !noticeIdentity(row.UserID) {
			err = ErrInvalidScope
			break
		}
		if _, exists := result[row.UserID]; exists {
			err = ErrConflict
			break
		}
		result[row.UserID] = row
	}
	if err == nil {
		err = cur.Err()
	}
	closed := cur.Close(ctx)
	if err != nil || closed != nil {
		return nil, errors.Join(err, closed)
	}
	return result, nil
}

func BeginMemberApplication(ctx context.Context, db *mongo.Database, gid string, profiles map[string]*sdkws.PublicUserInfo) (*MemberApplicationScope, error) {
	if ctx == nil || db == nil {
		return nil, ErrInvalidScope
	}
	c, ok := ctx.Value(memberApplicationKey{}).(*memberApplicationCommand)
	if !ok || c == nil || c.info.GroupID != gid || c.info.Actor != mcontext.GetOpUserID(ctx) || RequireMembers(ctx, db, gid, c.applicants) != nil {
		return nil, ErrInvalidScope
	}
	scope, ok := ctx.Value(scopeKey{}).(*mutationScope)
	if !ok || scope == nil || len(scope.targets) != 1 {
		return nil, ErrInvalidScope
	}
	target := scope.targets[gid]
	if target == nil || target.create || target.all || len(target.members) != len(c.applicants) {
		return nil, ErrInvalidScope
	}
	group, err := readNoticeGroup(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	if group.Status == constant.GroupStatusDismissed {
		return nil, ErrInvalidScope
	}
	members, err := readRoleRoster(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	actor := members[c.info.Actor]
	manager := authverify.IsAppManagerUid(ctx)
	if c.kind == MemberApplicationCreated {
		if !manager {
			for _, uid := range c.applicants {
				if uid != c.info.Actor && actor.UserID != c.info.Actor {
					return nil, ErrInvalidScope
				}
			}
		}
	} else if !manager && actor.RoleLevel != constant.GroupOwner && actor.RoleLevel != constant.GroupAdmin {
		return nil, ErrInvalidScope
	}
	requests, err := readMemberApplications(ctx, db, gid, c.applicants)
	if err != nil {
		return nil, err
	}
	if c.kind != MemberApplicationCreated {
		for _, uid := range c.applicants {
			row, ok := requests[uid]
			if !ok || row.HandleResult != 0 {
				return nil, ErrConflict
			}
		}
	}
	copied := map[string]*sdkws.PublicUserInfo{}
	for uid, value := range profiles {
		if value == nil || value.UserID != uid {
			return nil, ErrInvalidScope
		}
		copied[uid] = proto.Clone(value).(*sdkws.PublicUserInfo)
	}
	for _, uid := range c.applicants {
		if copied[uid] == nil {
			return nil, ErrInvalidScope
		}
	}
	return &MemberApplicationScope{scope: scope, session: mongo.SessionFromContext(ctx), command: c, preGroup: group, preMembers: members, pre: requests, profiles: copied, messages: map[string]MemberApplicationMessageInfo{}}, nil
}

func (cap *MemberApplicationScope) current(ctx context.Context, db *mongo.Database) error {
	if cap == nil || cap.command == nil || cap.scope == nil || cap.session == nil || ctx == nil || mongo.SessionFromContext(ctx) != cap.session || ctx.Value(scopeKey{}) != cap.scope || ctx.Value(memberApplicationKey{}) != cap.command || mcontext.GetOpUserID(ctx) != cap.command.info.Actor || RequireMembers(ctx, db, cap.command.info.GroupID, cap.command.applicants) != nil {
		return ErrInvalidScope
	}
	return nil
}
func (cap *MemberApplicationScope) Complete(ctx context.Context, db *mongo.Database) error {
	if cap.current(ctx, db) != nil || cap.finished {
		return ErrInvalidScope
	}
	gid := cap.command.info.GroupID
	group, err := readNoticeGroup(ctx, db, gid)
	if err != nil {
		return err
	}
	if group != cap.preGroup {
		return ErrConflict
	}
	members, err := readRoleRoster(ctx, db, gid)
	if err != nil {
		return err
	}
	for uid, before := range cap.preMembers {
		if after, ok := members[uid]; !ok || after != before {
			return ErrConflict
		}
	}
	for uid, member := range members {
		if _, exists := cap.preMembers[uid]; !exists {
			if cap.command.kind != MemberApplicationAccepted || len(cap.command.applicants) != 1 || uid != cap.command.applicants[0] || member.RoleLevel != constant.GroupOrdinaryUsers {
				return ErrConflict
			}
		}
	}
	requests, err := readMemberApplications(ctx, db, gid, cap.command.applicants)
	if err != nil {
		return err
	}
	if len(requests) != len(cap.command.applicants) {
		return ErrConflict
	}
	for _, uid := range cap.command.applicants {
		after, ok := requests[uid]
		if !ok || after.ReqTime.IsZero() {
			return ErrConflict
		}
		before, existed := cap.pre[uid]
		if cap.command.kind == MemberApplicationCreated {
			if after.HandleResult != 0 || after.HandleUserID != "" || existed && after == before {
				return ErrConflict
			}
		} else {
			expected := int32(constant.GroupResponseRefuse)
			if cap.command.kind == MemberApplicationAccepted {
				expected = constant.GroupResponseAgree
				if _, ok := members[uid]; !ok {
					return ErrConflict
				}
			}
			if !existed || after.HandleResult != expected || after.HandleUserID != cap.command.info.Actor || after.HandledTime.IsZero() || after.ReqTime != before.ReqTime || after.ReqMsg != before.ReqMsg || after.JoinSource != before.JoinSource || after.InviterUserID != before.InviterUserID || after.Ex != before.Ex {
				return ErrConflict
			}
		}
	}
	var raw bson.Raw
	if err = db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&raw); err != nil {
		return err
	}
	revision, ok := raw.Lookup(RevisionField).Int64OK()
	if !ok || revision < 1 {
		return ErrInvalidRevision
	}
	cap.postGroup, cap.postMembers, cap.post, cap.revision, cap.finished = group, members, requests, revision, true
	return nil
}
func (cap *MemberApplicationScope) Inspect(ctx context.Context, db *mongo.Database) (MemberApplicationInfo, error) {
	if cap.current(ctx, db) != nil || !cap.finished {
		return MemberApplicationInfo{}, ErrInvalidScope
	}
	group, err := readNoticeGroup(ctx, db, cap.command.info.GroupID)
	if err != nil {
		return MemberApplicationInfo{}, err
	}
	members, err := readRoleRoster(ctx, db, cap.command.info.GroupID)
	if err != nil {
		return MemberApplicationInfo{}, err
	}
	requests, err := readMemberApplications(ctx, db, cap.command.info.GroupID, cap.command.applicants)
	if err != nil {
		return MemberApplicationInfo{}, err
	}
	if group != cap.postGroup || !reflect.DeepEqual(members, cap.postMembers) || !reflect.DeepEqual(requests, cap.post) {
		return MemberApplicationInfo{}, ErrConflict
	}
	set := map[string]bool{cap.command.info.Actor: true}
	for uid, m := range members {
		if m.RoleLevel == constant.GroupOwner || m.RoleLevel == constant.GroupAdmin {
			set[uid] = true
		}
	}
	if cap.command.kind == MemberApplicationCreated {
		for _, uid := range cap.command.applicants {
			set[uid] = true
		}
	}
	recipients := make([]string, 0, len(set))
	for uid := range set {
		recipients = append(recipients, uid)
	}
	sort.Strings(recipients)
	return MemberApplicationInfo{Command: cap.command.info, Kind: cap.command.kind, GroupRevision: cap.revision, Applicants: append([]string(nil), cap.command.applicants...), Recipients: recipients}, nil
}

func (cap *MemberApplicationScope) Message(ctx context.Context, db *mongo.Database, applicant, receiver string, options map[string]bool) (*sdkws.MsgData, error) {
	info, err := cap.Inspect(ctx, db)
	if err != nil {
		return nil, err
	}
	request, ok := cap.post[applicant]
	if !ok {
		return nil, ErrInvalidScope
	}
	allowed := receiver == info.Command.Actor
	if m, ok := cap.postMembers[receiver]; ok && (m.RoleLevel == constant.GroupOwner || m.RoleLevel == constant.GroupAdmin) {
		allowed = true
	}
	if info.Kind == MemberApplicationCreated && receiver == applicant {
		allowed = true
	}
	if !allowed {
		return nil, ErrInvalidScope
	}
	owner := ""
	for uid, m := range cap.postMembers {
		if m.RoleLevel == constant.GroupOwner {
			owner = uid
		}
	}
	group := convert.Db2PbGroupInfo(&cap.postGroup, owner, uint32(len(cap.postMembers)))
	var tips proto.Message
	var kind int32
	op := &sdkws.GroupMemberFullInfo{GroupID: info.Command.GroupID, UserID: info.Command.Actor, AppMangerLevel: constant.AppAdmin}
	if member, ok := cap.postMembers[info.Command.Actor]; ok {
		op = convert.Db2PbGroupMember(&member)
	}
	if profile := cap.profiles[info.Command.Actor]; profile != nil {
		if op.Nickname == "" {
			op.Nickname = profile.Nickname
		}
		if op.FaceURL == "" {
			op.FaceURL = profile.FaceURL
		}
	}
	switch info.Kind {
	case MemberApplicationCreated:
		tips = &sdkws.JoinGroupApplicationTips{Group: group, Applicant: proto.Clone(cap.profiles[applicant]).(*sdkws.PublicUserInfo), ReqMsg: request.ReqMsg}
		kind = constant.JoinGroupApplicationNotification
	case MemberApplicationAccepted:
		tips = &sdkws.GroupApplicationAcceptedTips{Group: group, OpUser: op, HandleMsg: request.HandledMsg, ReceiverAs: 1}
		kind = constant.GroupApplicationAcceptedNotification
	case MemberApplicationRejected:
		tips = &sdkws.GroupApplicationRejectedTips{Group: group, OpUser: op, HandleMsg: request.HandledMsg}
		kind = constant.GroupApplicationRejectedNotification
	default:
		return nil, ErrInvalidScope
	}
	content, err := json.Marshal(&sdkws.NotificationElem{Detail: utils.StructToJsonString(tips)})
	if err != nil {
		return nil, err
	}
	n, _ := uuid.Parse(info.Command.ID)
	key := uuid.NewSHA1(n, []byte("direct-member-application\x00"+applicant+"\x00"+receiver)).String()
	copiedOptions := map[string]bool{}
	for k, v := range options {
		copiedOptions[k] = v
	}
	message := &sdkws.MsgData{ClientMsgID: key, SendID: info.Command.Actor, RecvID: receiver, MsgFrom: constant.SysMsgType, ContentType: kind, SessionType: constant.SingleChatType, CreateTime: info.Command.CreatedAt.UnixMilli(), Content: content, Options: copiedOptions, OfflinePushInfo: &sdkws.OfflinePushInfo{}}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	proof := MemberApplicationMessageInfo{Command: info.Command, Kind: info.Kind, Applicant: applicant, Recipient: receiver, MessageSHA256: hex.EncodeToString(sum[:])}
	if prior, exists := cap.messages[key]; exists && prior != proof {
		return nil, ErrConflict
	}
	cap.messages[key] = proof
	return message, nil
}

func (cap *MemberApplicationScope) RecipientsFor(ctx context.Context, db *mongo.Database, applicant string) ([]string, error) {
	info, err := cap.Inspect(ctx, db)
	if err != nil {
		return nil, err
	}
	if _, ok := cap.post[applicant]; !ok {
		return nil, ErrInvalidScope
	}
	var result []string
	for _, uid := range info.Recipients {
		if uid == info.Command.Actor || info.Kind == MemberApplicationCreated && uid == applicant {
			result = append(result, uid)
			continue
		}
		if m, ok := cap.postMembers[uid]; ok && (m.RoleLevel == constant.GroupOwner || m.RoleLevel == constant.GroupAdmin) {
			result = append(result, uid)
		}
	}
	return result, nil
}

func (cap *MemberApplicationScope) InspectMessage(ctx context.Context, db *mongo.Database, message *sdkws.MsgData) (MemberApplicationMessageInfo, error) {
	if _, err := cap.Inspect(ctx, db); err != nil {
		return MemberApplicationMessageInfo{}, err
	}
	if message == nil || message.SendID != cap.command.info.Actor || message.GroupID != "" || message.SessionType != constant.SingleChatType || message.MsgFrom != constant.SysMsgType || message.Seq != 0 || message.ServerMsgID != "" || message.SendTime != 0 {
		return MemberApplicationMessageInfo{}, ErrInvalidScope
	}
	proof, ok := cap.messages[message.ClientMsgID]
	if !ok || proof.Recipient != message.RecvID {
		return MemberApplicationMessageInfo{}, ErrInvalidScope
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return MemberApplicationMessageInfo{}, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != proof.MessageSHA256 {
		return MemberApplicationMessageInfo{}, ErrConflict
	}
	return proof, nil
}
