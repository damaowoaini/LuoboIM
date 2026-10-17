package deliveryprofile

import (
	"context"
	"encoding/json"
	"math"
	"sort"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type RemovalNoticeBranchInput struct {
	ID, CID, PayloadSHA256, ReadBindingSHA256 string
	Payload                                   []byte
	Persist                                   bool
}
type RemovalVisibilityBranch struct {
	CID           string   `bson:"conversation_id" json:"conversationID"`
	PayloadSHA256 string   `bson:"payload_sha256" json:"payloadSHA256"`
	Persist       bool     `bson:"persist" json:"persist"`
	Owners        []string `bson:"owners" json:"owners"`
}
type RemovalVisibilityMember struct {
	Normal       string `bson:"normal" json:"normal"`
	Notification string `bson:"notification" json:"notification"`
}

// This does not extend a membership window or the generic v2 cut. It is one
// exact original command/effect/branch grant, made from a live private removal
// capability in the original business transaction. Only hashes name owners.
type MemberRemovalVisibilityCut struct {
	Version       string                             `bson:"version" json:"version"`
	Base          VisibilityCut                      `bson:"base" json:"base"`
	EffectID      string                             `bson:"effect_id" json:"effectID"`
	CommandID     string                             `bson:"command_id" json:"commandID"`
	GroupID       string                             `bson:"group_id" json:"groupID"`
	Action        string                             `bson:"action" json:"action"`
	GroupRevision int64                              `bson:"group_revision" json:"groupRevision"`
	ProfileSHA256 string                             `bson:"profile_sha256" json:"profileSHA256"`
	Branches      map[string]RemovalVisibilityBranch `bson:"branches" json:"branches"`
	Members       map[string]RemovalVisibilityMember `bson:"members" json:"members"`
}
type RemovalVisibilityIdentity struct {
	EffectID, BranchID, CID, ProfileSHA256 string
	Payload                                []byte
}

func (c MemberRemovalVisibilityCut) Valid() bool {
	if c.Version != "member-removal-visibility-v1" || !c.Base.Valid() || !validID(c.EffectID) || !validID(c.CommandID) || !ordinaryCutText(c.GroupID) || c.GroupRevision < 1 || c.GroupRevision >= math.MaxInt64 || !validSHA(c.ProfileSHA256) || c.Action != string(groupauthority.NoticeKick) && c.Action != string(groupauthority.NoticeQuit) || c.Base.ConversationID != msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, c.GroupID) || len(c.Branches) < 1 || len(c.Branches) > 2 || len(c.Members) == 0 {
		return false
	}
	used := map[string]bool{}
	for owner, m := range c.Members {
		if !validSHA(owner) || !validSHA(m.Normal) || !validSHA(m.Notification) {
			return false
		}
		if _, ok := c.Base.Owners[owner]; !ok {
			return false
		}
	}
	for id, b := range c.Branches {
		if !validID(id) || b.CID != c.Base.ConversationID && b.CID != c.Base.Notification.ConversationID || !validSHA(b.PayloadSHA256) {
			return false
		}
		for i, owner := range b.Owners {
			if _, ok := c.Members[owner]; !ok || i > 0 && owner <= b.Owners[i-1] {
				return false
			}
			used[owner] = true
		}
	}
	return len(used) == len(c.Members)
}

func CaptureMemberRemovalVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation string, snapshot *groupauthority.NoticeSnapshot, cap *groupauthority.MemberRemovalScope, effectID, profileSHA string, plans []RemovalNoticeBranchInput) (MemberRemovalVisibilityCut, error) {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return MemberRemovalVisibilityCut{}, err
	}
	if snapshot == nil || cap == nil || !validID(effectID) || !validSHA(profileSHA) || len(plans) < 1 || len(plans) > 2 {
		return MemberRemovalVisibilityCut{}, ErrInvalid
	}
	info, err := cap.Inspect(ctx, db)
	if err != nil {
		return MemberRemovalVisibilityCut{}, err
	}
	command := snapshot.Command()
	if info.Command != command || command.Epoch != epoch || command.Action != groupauthority.NoticeKick && command.Action != groupauthority.NoticeQuit || info.GroupRevision < 1 {
		return MemberRemovalVisibilityCut{}, ErrInvalid
	}
	// Exact existing groupeffects ordinal0 notification recipe; no arbitrary
	// source UUID may borrow a removal command to mint a second message.
	parent, e := uuid.Parse(command.ID)
	if e != nil {
		return MemberRemovalVisibilityCut{}, ErrInvalid
	}
	identity, _ := json.Marshal([]any{int32(0), command.GroupID, command.Actor, "notification-v1"})
	if uuid.NewSHA1(parent, identity).String() != effectID {
		return MemberRemovalVisibilityCut{}, ErrInvalid
	}
	normalCID := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, command.GroupID)
	noticeCID := msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, command.GroupID)
	removed := map[string]bool{}
	for _, uid := range info.Members {
		removed[uid] = true
	}
	recipients := map[string]bool{}
	branches := map[string]RemovalVisibilityBranch{}
	effect, _ := uuid.Parse(effectID)
	for _, p := range plans {
		if branches[p.ID].CID != "" || !validSHA(p.PayloadSHA256) || SHA(p.Payload) != p.PayloadSHA256 || len(p.Payload) > 1<<20 {
			return MemberRemovalVisibilityCut{}, ErrInvalid
		}
		var message sdkws.MsgData
		if proto.Unmarshal(p.Payload, &message) != nil || message.Seq != 0 || message.SessionType != constant.SuperGroupChatType || message.GroupID != command.GroupID || message.RecvID != command.GroupID || message.SendID != command.Actor || message.MsgFrom != constant.SysMsgType || msgprocessor.GetConversationIDByMsg(&message) != p.CID || msgprocessor.Options(message.Options).IsHistory() != p.Persist {
			return MemberRemovalVisibilityCut{}, ErrInvalid
		}
		label := "chat-copy"
		if p.CID == noticeCID {
			label = "notification"
		} else if p.CID != normalCID {
			return MemberRemovalVisibilityCut{}, ErrInvalid
		}
		if uuid.NewSHA1(effect, []byte(label+"\x00"+p.CID)).String() != p.ID || message.ServerMsgID != uuid.NewSHA1(effect, []byte("server\x00"+p.ID)).String() {
			return MemberRemovalVisibilityCut{}, ErrInvalid
		}
		users, binding, err := snapshot.NotificationRecipients(ctx, db, p.ID, p.PayloadSHA256, message.ContentType, p.CID == noticeCID)
		if err != nil || binding != p.ReadBindingSHA256 {
			return MemberRemovalVisibilityCut{}, ErrInvalid
		}
		b := RemovalVisibilityBranch{CID: p.CID, PayloadSHA256: p.PayloadSHA256, Persist: p.Persist, Owners: []string{}}
		for _, uid := range users {
			recipients[uid] = true
			if removed[uid] {
				b.Owners = append(b.Owners, SHA([]byte(uid)))
			}
		}
		sort.Strings(b.Owners)
		branches[p.ID] = b
	}
	users := make([]string, 0, len(recipients))
	for uid := range recipients {
		users = append(users, uid)
	}
	sort.Strings(users)
	chat, err := capturePrivacyScope(ctx, db, epoch, normalCID, users)
	if err != nil {
		return MemberRemovalVisibilityCut{}, err
	}
	notice, err := capturePrivacyScope(ctx, db, epoch, noticeCID, users)
	if err != nil {
		return MemberRemovalVisibilityCut{}, err
	}
	result := MemberRemovalVisibilityCut{Version: "member-removal-visibility-v1", Base: VisibilityCut{Version: "owned-visibility-cut-v2", ConversationID: normalCID, GlobalRevision: chat.GlobalRevision, SourceHead: chat.SourceHead, Owners: chat.Owners, Members: map[string]string{}, Notification: &notice}, EffectID: effectID, CommandID: command.ID, GroupID: command.GroupID, Action: string(command.Action), GroupRevision: info.GroupRevision, ProfileSHA256: profileSHA, Branches: branches, Members: map[string]RemovalVisibilityMember{}}
	for start := 0; start < len(users); start += 64 {
		page := users[start:min(start+64, len(users))]
		normal, err := memberCutRows(ctx, db, epoch, normalCID, page, true)
		if err != nil {
			return MemberRemovalVisibilityCut{}, err
		}
		notify, err := memberCutRows(ctx, db, epoch, noticeCID, page, true)
		if err != nil {
			return MemberRemovalVisibilityCut{}, err
		}
		for _, uid := range page {
			a, b := normal[uid], notify[uid]
			if a.GID != command.GroupID || b.GID != command.GroupID {
				return MemberRemovalVisibilityCut{}, ErrInvalid
			}
			if removed[uid] {
				if !a.Closed || !b.Closed || a.Last != command.ID || b.Last != command.ID || a.GroupRevision != info.GroupRevision || b.GroupRevision != info.GroupRevision {
					return MemberRemovalVisibilityCut{}, ErrInvalid
				}
				result.Members[SHA([]byte(uid))] = RemovalVisibilityMember{Normal: removalWindowToken(a), Notification: removalWindowToken(b)}
			} else if a.Closed || b.Closed || a.Last != b.Last || a.GroupRevision != b.GroupRevision {
				return MemberRemovalVisibilityCut{}, ErrInvalid
			}
			result.Base.Members[SHA([]byte(uid))] = memberCutToken(a)
		}
	}
	if !result.Valid() {
		return MemberRemovalVisibilityCut{}, ErrInvalid
	}
	return result, nil
}

// Scalar coordinates are not authority: exact persisted cut, normalized source
// payload, profile and current original closed windows must ALL still match.
func CompareMemberRemovalVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation string, cut MemberRemovalVisibilityCut, call RemovalVisibilityIdentity, users []string, sequence, normalSequence int64, fence bool) (map[string]bool, error) {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return nil, err
	}
	if !cut.Valid() || call.EffectID != cut.EffectID || call.ProfileSHA256 != cut.ProfileSHA256 || len(users) > 64 || sequence < 0 || sequence == math.MaxInt64 || normalSequence < 0 || normalSequence == math.MaxInt64 {
		return nil, ErrInvalid
	}
	plan, ok := cut.Branches[call.BranchID]
	if !ok || plan.CID != call.CID || len(call.Payload) == 0 || len(call.Payload) > 1<<20 {
		return nil, ErrInvalid
	}
	var message sdkws.MsgData
	if proto.Unmarshal(call.Payload, &message) != nil || message.Seq != sequence || msgprocessor.GetConversationIDByMsg(&message) != call.CID || message.GroupID != cut.GroupID || message.SessionType != constant.SuperGroupChatType || msgprocessor.Options(message.Options).IsHistory() != plan.Persist || !plan.Persist && sequence != 0 {
		return nil, ErrInvalid
	}
	message.Seq = 0
	normalized, err := proto.MarshalOptions{Deterministic: true}.Marshal(&message)
	if err != nil || SHA(normalized) != plan.PayloadSHA256 {
		return nil, ErrInvalid
	}
	base := cut.Base
	position := sequence
	chatPosition := normalSequence
	if call.CID == base.ConversationID {
		if normalSequence != 0 && normalSequence != sequence {
			return nil, ErrInvalid
		}
		chatPosition = sequence
	}
	denied, err := comparePrivacyScope(ctx, db, epoch, PrivacyScopeCut{ConversationID: base.ConversationID, GlobalRevision: base.GlobalRevision, SourceHead: base.SourceHead, Owners: base.Owners}, users, chatPosition, fence)
	if err != nil {
		return nil, err
	}
	if call.CID == base.Notification.ConversationID {
		other, err := comparePrivacyScope(ctx, db, epoch, *base.Notification, users, sequence, fence)
		if err != nil {
			return nil, err
		}
		for uid, value := range other {
			denied[uid] = denied[uid] || value
		}
	}
	if position == 0 {
		if call.CID == base.ConversationID {
			position = base.SourceHead + 1
		} else {
			position = base.Notification.SourceHead + 1
		}
	}
	normal, err := memberCutRows(ctx, db, epoch, base.ConversationID, users, fence)
	if err != nil {
		return nil, err
	}
	notice, err := memberCutRows(ctx, db, epoch, base.Notification.ConversationID, users, fence)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, owner := range plan.Owners {
		allowed[owner] = true
	}
	seen := map[string]bool{}
	for _, uid := range users {
		owner := SHA([]byte(uid))
		if !ordinaryCutText(uid) || seen[uid] {
			return nil, ErrInvalid
		}
		seen[uid] = true
		if _, ok := base.Owners[owner]; !ok {
			return nil, ErrInvalid
		}
		a, b := normal[uid], notice[uid]
		memberDenied := false
		if original, removed := cut.Members[owner]; removed {
			memberDenied = !allowed[owner] || !a.Closed || !b.Closed || a.Last != cut.CommandID || b.Last != cut.CommandID || a.GroupRevision != cut.GroupRevision || b.GroupRevision != cut.GroupRevision || removalWindowToken(a) != original.Normal || removalWindowToken(b) != original.Notification
		} else {
			actual := a
			if call.CID == base.Notification.ConversationID {
				actual = b
			}
			memberDenied = a.Closed || b.Closed || a.GID != cut.GroupID || b.GID != cut.GroupID || a.Last != b.Last || a.GroupRevision != b.GroupRevision || memberCutToken(a) != base.Members[owner] || position < actual.Lower
		}
		denied[uid] = denied[uid] || memberDenied
	}
	return denied, nil
}

func (c MemberRemovalVisibilityCut) Clone() MemberRemovalVisibilityCut {
	out := c
	out.Base.Owners = map[string]int64{}
	out.Base.Members = map[string]string{}
	for k, v := range c.Base.Owners {
		out.Base.Owners[k] = v
	}
	for k, v := range c.Base.Members {
		out.Base.Members[k] = v
	}
	if c.Base.Notification != nil {
		n := *c.Base.Notification
		n.Owners = map[string]int64{}
		for k, v := range c.Base.Notification.Owners {
			n.Owners[k] = v
		}
		out.Base.Notification = &n
	}
	out.Branches = map[string]RemovalVisibilityBranch{}
	for k, v := range c.Branches {
		v.Owners = append([]string{}, v.Owners...)
		out.Branches[k] = v
	}
	out.Members = map[string]RemovalVisibilityMember{}
	for k, v := range c.Members {
		out.Members[k] = v
	}
	return out
}

func (c *MemberRemovalVisibilityCut) UnmarshalBSON(data []byte) error {
	raw := bson.Raw(data)
	// All ten fields, including typed nested documents, are exact.
	elements, e := raw.Elements()
	if e != nil || len(elements) != 10 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, field := range elements {
		if seen[field.Key()] {
			return ErrInvalid
		}
		seen[field.Key()] = true
		switch field.Key() {
		case "version", "effect_id", "command_id", "group_id", "action", "profile_sha256":
			if _, ok := field.Value().StringValueOK(); !ok {
				return ErrInvalid
			}
		case "group_revision":
			if _, ok := field.Value().Int64OK(); !ok {
				return ErrInvalid
			}
		case "base":
			doc, ok := field.Value().DocumentOK()
			if !ok {
				return ErrInvalid
			}
			var base VisibilityCut
			if bson.Unmarshal(doc, &base) != nil {
				return ErrInvalid
			}
		case "members":
			doc, ok := field.Value().DocumentOK()
			if !ok {
				return ErrInvalid
			}
			fields, e := doc.Elements()
			if e != nil {
				return ErrInvalid
			}
			owners := map[string]bool{}
			for _, f := range fields {
				v, ok := f.Value().DocumentOK()
				if !ok || owners[f.Key()] || !validSHA(f.Key()) || !rawCutShape(v, []string{"normal", "notification"}, nil, nil) {
					return ErrInvalid
				}
				owners[f.Key()] = true
			}
		case "branches":
			doc, ok := field.Value().DocumentOK()
			if !ok {
				return ErrInvalid
			}
			fields, e := doc.Elements()
			if e != nil {
				return ErrInvalid
			}
			ids := map[string]bool{}
			for _, f := range fields {
				v, ok := f.Value().DocumentOK()
				if !ok || ids[f.Key()] || !validID(f.Key()) {
					return ErrInvalid
				}
				ids[f.Key()] = true
				elements, e := v.Elements()
				if e != nil || len(elements) != 4 {
					return ErrInvalid
				}
				keys := map[string]bool{}
				for _, a := range elements {
					if keys[a.Key()] {
						return ErrInvalid
					}
					keys[a.Key()] = true
					switch a.Key() {
					case "conversation_id", "payload_sha256":
						if _, ok := a.Value().StringValueOK(); !ok {
							return ErrInvalid
						}
					case "persist":
						if _, ok := a.Value().BooleanOK(); !ok {
							return ErrInvalid
						}
					case "owners":
						array, ok := a.Value().ArrayOK()
						if !ok {
							return ErrInvalid
						}
						values, e := array.Values()
						if e != nil {
							return ErrInvalid
						}
						for _, value := range values {
							if _, ok := value.StringValueOK(); !ok {
								return ErrInvalid
							}
						}
					default:
						return ErrInvalid
					}
				}
			}
		default:
			return ErrInvalid
		}
	}
	type plain MemberRemovalVisibilityCut
	var parsed plain
	if bson.Unmarshal(data, &parsed) != nil {
		return ErrInvalid
	}
	*c = MemberRemovalVisibilityCut(parsed)
	if !c.Valid() {
		return ErrInvalid
	}
	return nil
}
