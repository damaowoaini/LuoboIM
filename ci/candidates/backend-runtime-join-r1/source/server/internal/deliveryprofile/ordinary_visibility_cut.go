package deliveryprofile

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// This is a source custody cut, never a group-effect/profile capability or a
// positive-history entitlement. Direct and server-notification identities come
// from the original typed message, not from splitting an ambiguous CID.
type OrdinaryVisibilityCut struct {
	Version       string            `bson:"version" json:"version"`
	Normal        PrivacyScopeCut   `bson:"normal" json:"normal"`
	Branch        PrivacyScopeCut   `bson:"branch" json:"branch"`
	MembershipCID string            `bson:"membership_cid" json:"membershipCID"`
	Members       map[string]string `bson:"members" json:"members"`
}

func ordinaryCutText(v string) bool {
	return v != "" && len(v) <= 512 && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\x00\r\n")
}

func ordinaryCutCIDs(message *sdkws.MsgData) (normal, branch, member string, err error) {
	if message == nil || !ordinaryCutText(message.SendID) || !ordinaryCutText(message.RecvID) {
		return "", "", "", ErrInvalid
	}
	switch message.SessionType {
	case constant.SingleChatType, constant.NotificationChatType:
		if message.GroupID != "" {
			return "", "", "", ErrInvalid
		}
	case constant.GroupChatType, constant.SuperGroupChatType:
		if !ordinaryCutText(message.GroupID) || message.RecvID != message.GroupID {
			return "", "", "", ErrInvalid
		}
		member = msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, message.GroupID)
	default:
		return "", "", "", ErrInvalid
	}
	normal = msgprocessor.GetChatConversationIDByMsg(message)
	branch = msgprocessor.GetConversationIDByMsg(message)
	notification := msgprocessor.GetNotificationConversationIDByMsg(message)
	if !ordinaryCutText(normal) || !ordinaryCutText(branch) || !ordinaryCutText(notification) || branch != normal && branch != notification {
		return "", "", "", ErrInvalid
	}
	return normal, branch, member, nil
}

func validOrdinaryPrivacyScope(cut PrivacyScopeCut) bool {
	if !ordinaryCutText(cut.ConversationID) || cut.GlobalRevision < 0 || cut.GlobalRevision >= math.MaxInt64-1 || cut.SourceHead < 0 || cut.SourceHead >= math.MaxInt64-1 || cut.Owners == nil {
		return false
	}
	for owner, revision := range cut.Owners {
		if !validSHA(owner) || revision < 0 || revision >= math.MaxInt64-1 {
			return false
		}
	}
	return true
}

func (cut OrdinaryVisibilityCut) Valid() bool {
	if cut.Version != "ordinary-visibility-cut-v1" || !validOrdinaryPrivacyScope(cut.Normal) || !validOrdinaryPrivacyScope(cut.Branch) || cut.Members == nil || len(cut.Normal.Owners) != len(cut.Branch.Owners) {
		return false
	}
	for owner := range cut.Normal.Owners {
		if _, ok := cut.Branch.Owners[owner]; !ok {
			return false
		}
	}
	if cut.Normal.ConversationID == cut.Branch.ConversationID {
		if Digest(cut.Normal) != Digest(cut.Branch) {
			return false
		}
	}
	if cut.MembershipCID == "" {
		return len(cut.Members) == 0
	}
	if !ordinaryCutText(cut.MembershipCID) || len(cut.Members) != len(cut.Normal.Owners) {
		return false
	}
	for owner, command := range cut.Members {
		if _, ok := cut.Normal.Owners[owner]; !ok || !validSHA(command) {
			return false
		}
	}
	return true
}

// Matches only checks the original typed identity. It never reads a new roster
// or grants delivery on its own; every recipient still needs Compare below.
func (cut OrdinaryVisibilityCut) Matches(message *sdkws.MsgData) bool {
	normal, branch, member, err := ordinaryCutCIDs(message)
	if err != nil || !cut.Valid() || cut.Normal.ConversationID != normal || cut.Branch.ConversationID != branch || cut.MembershipCID != member {
		return false
	}
	if member == "" {
		allowed := map[string]bool{SHA([]byte(message.SendID)): true, SHA([]byte(message.RecvID)): true}
		for owner := range cut.Normal.Owners {
			if !allowed[owner] {
				return false
			}
		}
	}
	return true
}

// Both scopes use the already prepared origin/counter pair. A missing counter
// is not an empty history and is never initialized here. Each scope/member read
// also fences the same original row in the enclosing strong source transaction.
func CaptureOrdinaryVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation string, message *sdkws.MsgData, users []string) (OrdinaryVisibilityCut, error) {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return OrdinaryVisibilityCut{}, err
	}
	normal, branch, member, err := ordinaryCutCIDs(message)
	if err != nil {
		return OrdinaryVisibilityCut{}, err
	}
	ordered := append([]string(nil), users...)
	sort.Strings(ordered)
	for i, user := range ordered {
		if !ordinaryCutText(user) || i > 0 && user == ordered[i-1] || member == "" && user != message.SendID && user != message.RecvID {
			return OrdinaryVisibilityCut{}, ErrInvalid
		}
	}
	normalCut, err := capturePrivacyScope(ctx, db, epoch, normal, ordered)
	if err != nil {
		return OrdinaryVisibilityCut{}, err
	}
	branchCut := normalCut
	if normal != branch {
		branchCut, err = capturePrivacyScope(ctx, db, epoch, branch, ordered)
		if err != nil {
			return OrdinaryVisibilityCut{}, err
		}
	}
	cut := OrdinaryVisibilityCut{Version: "ordinary-visibility-cut-v1", Normal: normalCut, Branch: branchCut, MembershipCID: member, Members: map[string]string{}}
	if member != "" {
		for start := 0; start < len(ordered); start += 64 {
			page, err := memberCuts(ctx, db, epoch, member, ordered[start:min(start+64, len(ordered))], nil, true)
			if err != nil {
				return OrdinaryVisibilityCut{}, err
			}
			for owner, command := range page {
				cut.Members[SHA([]byte(owner))] = command
			}
		}
	}
	if !cut.Matches(message) {
		return OrdinaryVisibilityCut{}, ErrInvalid
	}
	return cut, nil
}

func CompareOrdinaryVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation string, cut OrdinaryVisibilityCut, message *sdkws.MsgData, users []string, normalSequence int64, fence bool) (map[string]bool, error) {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return nil, err
	}
	if !cut.Matches(message) || message.Seq != 0 || normalSequence < 0 || normalSequence >= math.MaxInt64 || len(users) > 64 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, user := range users {
		if !ordinaryCutText(user) || seen[user] {
			return nil, ErrInvalid
		}
		if _, ok := cut.Normal.Owners[SHA([]byte(user))]; !ok {
			return nil, ErrInvalid
		}
		seen[user] = true
	}
	// A normal Seq0 branch has no stored peer. Notification clones may bind the
	// positive normal peer from the SAME acceptance receipt without turning the
	// Seq0 payload itself into history.
	if cut.Normal.ConversationID == cut.Branch.ConversationID && normalSequence != 0 {
		return nil, ErrInvalid
	}
	denied, err := comparePrivacyScope(ctx, db, epoch, cut.Normal, users, normalSequence, fence)
	if err != nil {
		return nil, err
	}
	if cut.Normal.ConversationID != cut.Branch.ConversationID {
		notificationDenied, err := comparePrivacyScope(ctx, db, epoch, cut.Branch, users, 0, fence)
		if err != nil {
			return nil, err
		}
		for owner, value := range notificationDenied {
			denied[owner] = denied[owner] || value
		}
	}
	if cut.MembershipCID != "" {
		position := message.Seq
		if position == 0 {
			position = cut.Branch.SourceHead + 1
		}
		members, err := memberCutsAt(ctx, db, epoch, cut.MembershipCID, cut.Branch.ConversationID, users, cut.Members, position, fence)
		if err != nil {
			return nil, err
		}
		for _, owner := range users {
			denied[owner] = denied[owner] || members[owner] == ""
		}
	}
	return denied, nil
}

// Persisted cuts reject missing/extra/duplicate keys and non-BSON-long counters,
// rather than decoding malformed snapshots to revision/head zero.
func (cut *OrdinaryVisibilityCut) UnmarshalBSON(data []byte) error {
	raw := bson.Raw(data)
	fields, err := raw.Elements()
	if err != nil || len(fields) != 5 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if seen[field.Key()] {
			return ErrInvalid
		}
		seen[field.Key()] = true
		switch field.Key() {
		case "version", "membership_cid":
			if _, ok := field.Value().StringValueOK(); !ok {
				return ErrInvalid
			}
		case "normal", "branch":
			doc, ok := field.Value().DocumentOK()
			if !ok || !ordinaryPrivacyShape(doc) {
				return ErrInvalid
			}
		case "members":
			doc, ok := field.Value().DocumentOK()
			if !ok {
				return ErrInvalid
			}
			entries, err := doc.Elements()
			if err != nil {
				return ErrInvalid
			}
			owners := map[string]bool{}
			for _, entry := range entries {
				value, ok := entry.Value().StringValueOK()
				if owners[entry.Key()] || !validSHA(entry.Key()) || !ok || !validSHA(value) {
					return ErrInvalid
				}
				owners[entry.Key()] = true
			}
		default:
			return ErrInvalid
		}
	}
	type plain OrdinaryVisibilityCut
	var decoded plain
	if bson.Unmarshal(data, &decoded) != nil {
		return ErrInvalid
	}
	*cut = OrdinaryVisibilityCut(decoded)
	if !cut.Valid() {
		return ErrInvalid
	}
	return nil
}

func ordinaryPrivacyShape(raw bson.Raw) bool {
	fields, err := raw.Elements()
	if err != nil || len(fields) != 4 {
		return false
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if seen[field.Key()] {
			return false
		}
		seen[field.Key()] = true
		switch field.Key() {
		case "conversation_id":
			if _, ok := field.Value().StringValueOK(); !ok {
				return false
			}
		case "global_revision", "source_head":
			if _, ok := field.Value().Int64OK(); !ok {
				return false
			}
		case "owners":
			doc, ok := field.Value().DocumentOK()
			if !ok {
				return false
			}
			entries, err := doc.Elements()
			if err != nil {
				return false
			}
			owners := map[string]bool{}
			for _, entry := range entries {
				value, ok := entry.Value().Int64OK()
				if owners[entry.Key()] || !validSHA(entry.Key()) || !ok || value < 0 || value >= math.MaxInt64-1 {
					return false
				}
				owners[entry.Key()] = true
			}
		default:
			return false
		}
	}
	return true
}
