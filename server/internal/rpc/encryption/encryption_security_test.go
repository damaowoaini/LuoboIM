package encryption

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	pb "github.com/OpenIMSDK/protocol/encryption"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/redis/go-redis/v9"
	"strconv"
	"time"
)

type keyDB struct {
	controller.EncryptionDatabase
	read      bool
	generated bool
}

func (d *keyDB) GetConversationEncryptionAllKey(context.Context, string) ([]*pb.VersionKey, error) {
	d.read = true
	return []*pb.VersionKey{{Version: 1, Key: "old-legacy-key-must-stay-unchanged"}, {Version: 2, Key: "another-existing-version"}}, nil
}
func (d *keyDB) GetConversationVersionKey(context.Context, string, int32) (string, error) {
	d.read = true
	return "old-legacy-key-must-stay-unchanged", nil
}
func (d *keyDB) GenConversationNextVersionKey(context.Context, string, func() (string, error)) error {
	d.generated = true
	return nil
}

type conversationStub struct {
	value *conversation.Conversation
	err   error
}

func (c conversationStub) GetConversation(context.Context, string, string) (*conversation.Conversation, error) {
	return c.value, c.err
}

type historyStub struct {
	member bool
	left   *time.Time
}

func (h historyStub) Current(context.Context, string, string) (bool, error) { return h.member, nil }
func (h historyStub) ObserveCurrent(context.Context, string, string) error  { return nil }
func (h historyStub) LastLeft(context.Context, string, string) (*time.Time, error) {
	if h.left == nil {
		return nil, errors.New("no reliable history")
	}
	return h.left, nil
}

type keyTimeRedis struct {
	redis.UniversalClient
	future string
}

func (r keyTimeRedis) HGet(_ context.Context, _ string, field string) *redis.StringCmd {
	if field == "ISSUED_AT:2" {
		return redis.NewStringResult(r.future, nil)
	}
	return redis.NewStringResult("", redis.Nil)
}
func TestEncryptionKeyReadChecksParticipantAndKeepsHistoricalVersions(t *testing.T) {
	for _, tc := range []struct {
		name, actor, id string
		conv            *conversation.Conversation
		member, allowed bool
	}{
		{name: "single participant", actor: "u1", id: "si_u1_u2", conv: &conversation.Conversation{OwnerUserID: "u1", UserID: "u2", ConversationID: "si_u1_u2", ConversationType: constant.SingleChatType}, allowed: true},
		{name: "other conversation", actor: "attacker", id: "si_u1_u2", conv: &conversation.Conversation{OwnerUserID: "victim", UserID: "u2", ConversationID: "si_u1_u2", ConversationType: constant.SingleChatType}},
		{name: "forged own conversation row", actor: "attacker", id: "si_u1_u2", conv: &conversation.Conversation{OwnerUserID: "attacker", UserID: "u2", ConversationID: "si_u1_u2", ConversationType: constant.SingleChatType}},
		{name: "current group member", actor: "u1", id: "sg_group", conv: &conversation.Conversation{OwnerUserID: "u1", GroupID: "group", ConversationID: "sg_group", ConversationType: constant.SuperGroupChatType}, member: true, allowed: true},
		{name: "former group member", actor: "u1", id: "sg_group", conv: &conversation.Conversation{OwnerUserID: "u1", GroupID: "group", ConversationID: "sg_group", ConversationType: constant.SuperGroupChatType}},
		{name: "anonymous", actor: "", id: "si_u1_u2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &keyDB{}
			server := &encryptionServer{conversationDatabase: db, conversations: conversationStub{value: tc.conv}, history: historyStub{member: tc.member}}
			ctx := context.WithValue(context.Background(), constant.OpUserID, tc.actor)
			resp, err := server.GetEncryptionKey(ctx, &pb.GetEncryptionKeyReq{ConversationID: tc.id})
			if (err == nil) != tc.allowed || db.read != tc.allowed {
				t.Fatal("key authorization failed", err)
			}
			if tc.allowed && (len(resp.VersionKeyList) != 2 || resp.VersionKeyList[0].Version != 1 || resp.VersionKeyList[0].Key != "old-legacy-key-must-stay-unchanged") {
				t.Fatal("historical key changed")
			}
		})
	}
}
func TestNormalUserCannotGenerateKeyVersion(t *testing.T) {
	db := &keyDB{}
	server := &encryptionServer{conversationDatabase: db}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "ordinary-user")
	if _, err := server.GenEncryptionKey(ctx, &pb.GenEncryptionKeyReq{ConversationID: "si_u1_u2"}); err == nil || db.generated {
		t.Fatal("user rotated an unrelated conversation key")
	}
}

func TestVerifiedFormerMemberCanDecryptOldVersionsButNotLaterKeys(t *testing.T) {
	left := time.Now().Add(-time.Hour)
	db := &keyDB{}
	server := &encryptionServer{conversationDatabase: db, conversations: conversationStub{value: &conversation.Conversation{OwnerUserID: "u1", GroupID: "group", ConversationID: "sg_group", ConversationType: constant.SuperGroupChatType}}, history: historyStub{left: &left}, rdb: keyTimeRedis{future: strconv.FormatInt(time.Now().UnixMilli(), 10)}}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "u1")
	resp, err := server.GetEncryptionKey(ctx, &pb.GetEncryptionKeyReq{ConversationID: "sg_group"})
	if err != nil || len(resp.VersionKeyList) != 1 || resp.VersionKeyList[0].Version != 1 {
		t.Fatal("verified former member lost old keys or received post-exit key", err)
	}
	db.read = false
	if _, err := server.GetEncryptionKey(ctx, &pb.GetEncryptionKeyReq{ConversationID: "sg_group", KeyVersion: 2}); err == nil || db.read {
		t.Fatal("post-exit version was disclosed")
	}
	if _, err := server.GetEncryptionKey(ctx, &pb.GetEncryptionKeyReq{ConversationID: "sg_group", KeyVersion: 1}); err != nil {
		t.Fatal("legacy historical key rejected", err)
	}
}
