// Package groupcleanup implements a typed terminal-group obligation. It does
// not replay group RPCs/callbacks, discover future recipients, or guess floors.
package groupcleanup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"google.golang.org/protobuf/proto"
)

const Collection = "group_dismiss_cleanup_v1"
const SchemaID = "schema:group-dismiss-cleanup-v1"

var (
	ErrInvalid     = errors.New("invalid group dismissal cleanup")
	ErrUnavailable = errors.New("group dismissal cleanup unavailable")
	ErrDependency  = errors.New("group dismissal notification acceptance pending")
	ErrConflict    = errors.New("group dismissal cleanup scope changed")
)

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func validSHA(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func identity(v string) bool {
	return v != "" && len(v) <= 256 && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\x00\r\n")
}

// The cleanup ID derives from the exact frozen notification, never from a new
// random retry. It can be prepared before the group mutation's retry loop.
type Intent struct {
	ID               string    `bson:"id" json:"id"`
	GroupID          string    `bson:"group_id" json:"groupID"`
	Actor            string    `bson:"actor" json:"actor"`
	SourceEffectID   string    `bson:"source_effect_id" json:"sourceEffectID"`
	SourcePayloadSHA string    `bson:"source_payload_sha256" json:"sourcePayloadSHA256"`
	CreatedAt        time.Time `bson:"created_at" json:"createdAt"`
}

func IntentFromDismissNotification(f groupeffects.Frozen) (Intent, error) {
	plans, err := groupeffects.PlanNotificationBranches(f)
	if err != nil || len(plans) == 0 {
		return Intent{}, ErrInvalid
	}
	var m sdkws.MsgData
	if proto.Unmarshal(f.Payload, &m) != nil || f.Kind != groupeffects.Notification || m.ContentType != constant.GroupDismissedNotification || m.MsgFrom != constant.SysMsgType || m.SessionType != constant.SuperGroupChatType || m.GroupID != f.GroupID || m.RecvID != f.GroupID {
		return Intent{}, ErrInvalid
	}
	parent, err := uuid.Parse(f.ID)
	if err != nil {
		return Intent{}, ErrInvalid
	}
	return Intent{ID: uuid.NewSHA1(parent, []byte("group-dismiss-cleanup-v1")).String(), GroupID: f.GroupID, Actor: f.Actor, SourceEffectID: f.ID, SourcePayloadSHA: f.PayloadSHA256, CreatedAt: f.CreatedAt}, nil
}
func (i Intent) valid() bool {
	if !validID(i.ID) || !validID(i.SourceEffectID) || !identity(i.GroupID) || !identity(i.Actor) || !validSHA(i.SourcePayloadSHA) || i.CreatedAt.IsZero() || !i.CreatedAt.Equal(i.CreatedAt.UTC().Truncate(time.Millisecond)) {
		return false
	}
	parent, _ := uuid.Parse(i.SourceEffectID)
	return i.ID == uuid.NewSHA1(parent, []byte("group-dismiss-cleanup-v1")).String()
}
func canonicalMembers(input []string) ([]string, string, error) {
	if len(input) > 50000 {
		return nil, "", ErrInvalid
	}
	members := append([]string(nil), input...)
	sort.Strings(members)
	for i, id := range members {
		if !identity(id) || i > 0 && members[i-1] == id {
			return nil, "", ErrInvalid
		}
	}
	body, err := json.Marshal(members)
	if err != nil {
		return nil, "", ErrInvalid
	}
	return members, digest(body), nil
}

type branchProof struct {
	ID  string `bson:"id" json:"id"`
	CID string `bson:"conversation_id" json:"conversationID"`
}
type obligation struct {
	ID          string        `bson:"_id"`
	Epoch       string        `bson:"epoch"`
	Intent      Intent        `bson:"intent"`
	InputSHA    string        `bson:"input_sha256"`
	Branches    []branchProof `bson:"branches"`
	Members     []string      `bson:"members,omitempty"`
	MemberSHA   string        `bson:"member_sha256"`
	MemberCount int64         `bson:"member_count"`
	FrozenSHA   string        `bson:"frozen_sha256"`
	State       string        `bson:"state"`
	Revision    int64         `bson:"revision"`
	CompletedAt time.Time     `bson:"completed_at,omitempty"`
}

func (r obligation) proof() string {
	// Hashes bind original roster without retaining it after known cleanup.
	body, _ := json.Marshal(struct {
		Epoch       string
		Intent      Intent
		InputSHA    string
		Branches    []branchProof
		MemberSHA   string
		MemberCount int64
	}{r.Epoch, r.Intent, r.InputSHA, r.Branches, r.MemberSHA, r.MemberCount})
	return digest(body)
}
func (r obligation) valid(epoch string) bool {
	if !validID(epoch) || r.Epoch != epoch || r.ID != r.Intent.ID || !r.Intent.valid() || !validSHA(r.InputSHA) || !validSHA(r.MemberSHA) || r.MemberCount < 0 || r.MemberCount > 50000 || r.FrozenSHA != r.proof() || r.Revision < 1 || r.Revision > 2 || len(r.Branches) < 1 || len(r.Branches) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, b := range r.Branches {
		if !validID(b.ID) || seen[b.ID] || (b.CID != "sg_"+r.Intent.GroupID && b.CID != "n_"+r.Intent.GroupID) {
			return false
		}
		seen[b.ID] = true
	}
	if r.State == "done" {
		return r.Revision == 2 && len(r.Members) == 0 && !r.CompletedAt.IsZero()
	}
	if r.State != "pending" || r.Revision != 1 || !r.CompletedAt.IsZero() {
		return false
	}
	members, sha, err := canonicalMembers(r.Members)
	return err == nil && int64(len(members)) == r.MemberCount && sha == r.MemberSHA
}
