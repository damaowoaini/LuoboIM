package adminpreview

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"
	"time"
)

type sourceFixture struct {
	messages map[int64]*sdkws.MsgData
	maximum  int64
	reads    int
	badOwner bool
	key      string
}

func (f *sourceFixture) List(context.Context, string) ([]Conversation, error) {
	return []Conversation{{ConversationID: "a", UserID: "a", ShowName: "Alpha"}, {ConversationID: "b", UserID: "b", ShowName: "Beta"}, {ConversationID: "c", GroupID: "group-z", ShowName: "Gamma"}}, nil
}
func (f *sourceFixture) Get(_ context.Context, user, conversation string) (*relation.ConversationModel, error) {
	owner := user
	if f.badOwner {
		owner = "other"
	}
	return &relation.ConversationModel{OwnerUserID: owner, ConversationID: conversation, ConversationType: constant.SingleChatType, UserID: "peer"}, nil
}
func (f *sourceFixture) Bounds(context.Context, *relation.ConversationModel) (int64, int64, error) {
	return 1, f.maximum, nil
}
func (f *sourceFixture) Read(_ context.Context, _ string, _ string, seqs []int64) ([]*sdkws.MsgData, error) {
	f.reads++
	out := []*sdkws.MsgData{}
	for _, seq := range seqs {
		if value := f.messages[seq]; value != nil {
			out = append(out, value)
		}
	}
	return out, nil
}
func (f *sourceFixture) Key(context.Context, string, int32) (string, error) {
	if f.key == "" {
		return "", redis.Nil
	}
	return f.key, nil
}

type auditFixture struct {
	beginFail, finishFail bool
	started, finished     int
}

func (a *auditFixture) Begin(context.Context, string, string, Request) (string, error) {
	a.started++
	if a.beginFail {
		return "", errors.New("audit unavailable")
	}
	return "audit", nil
}
func (a *auditFixture) Finish(context.Context, string, string, int, int) error {
	a.finished++
	if a.finishFail {
		return errors.New("audit unavailable")
	}
	return nil
}
func fixtureService() (*Service, *sourceFixture, *auditFixture) {
	source := &sourceFixture{maximum: 3, messages: map[int64]*sdkws.MsgData{1: {Seq: 1, ContentType: constant.Text, Content: []byte(`{"content":"one"}`)}, 2: {Seq: 2, ContentType: constant.Text, Content: []byte(`{"content":"two"}`)}, 3: {Seq: 3, ContentType: constant.Text, Content: []byte(`{"content":"three"}`)}}}
	audit := &auditFixture{}
	return &Service{Source: source, Audit: audit, CursorKey: []byte(strings.Repeat("k", 32))}, source, audit
}
func TestPreviewCursorBindsActorTargetFiltersAndSnapshot(t *testing.T) {
	service, source, _ := fixtureService()
	req := Request{UserID: "owner", ConversationID: "si_owner_peer", PageSize: 2}
	first, err := service.Search(context.Background(), "admin", req)
	if err != nil || len(first.Messages) != 2 || first.Messages[0]["seq"] != int64(3) || !first.HasMore {
		t.Fatal("first page invalid", err)
	}
	source.maximum = 4
	source.messages[4] = &sdkws.MsgData{Seq: 4, ContentType: constant.Text, Content: []byte(`{"content":"new"}`)}
	req.Cursor = first.NextCursor
	next, err := service.Search(context.Background(), "admin", req)
	if err != nil || len(next.Messages) != 1 || next.Messages[0]["seq"] != int64(1) || next.HasMore {
		t.Fatal("continuation skipped/duplicated snapshot items", err)
	}
	if _, err := service.Search(context.Background(), "other-admin", req); err == nil {
		t.Fatal("cursor crossed admin identity")
	}
	changed := req
	changed.UserID = "victim"
	if _, err := service.Search(context.Background(), "admin", changed); err == nil {
		t.Fatal("cursor crossed target")
	}
	changed = req
	changed.Keyword = "two"
	if _, err := service.Search(context.Background(), "admin", changed); err == nil {
		t.Fatal("cursor crossed filters")
	}
	changed = req
	changed.Cursor += "x"
	if _, err := service.Search(context.Background(), "admin", changed); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	service.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := service.Search(context.Background(), "admin", req); err == nil {
		t.Fatal("expired cursor accepted")
	}
}
func TestPreviewAuditFailureAndOwnerScopeFailClosed(t *testing.T) {
	service, source, audit := fixtureService()
	req := Request{UserID: "owner", ConversationID: "si_owner_peer"}
	audit.beginFail = true
	if _, err := service.Search(context.Background(), "admin", req); err == nil || source.reads != 0 {
		t.Fatal("read occurred before durable audit")
	}
	audit.beginFail = false
	source.badOwner = true
	if _, err := service.Search(context.Background(), "admin", req); err == nil || source.reads != 0 {
		t.Fatal("foreign owner was read")
	}
	source.badOwner = false
	audit.finishFail = true
	if result, err := service.Search(context.Background(), "admin", req); err == nil || result != nil {
		t.Fatal("data returned without final audit persistence")
	}
}
func TestPreviewBoundedKeywordScanAndEncryptedUnavailability(t *testing.T) {
	service, source, _ := fixtureService()
	source.maximum = 2101
	source.messages = map[int64]*sdkws.MsgData{1: {Seq: 1, ContentType: constant.Text, Content: []byte(`{"content":"needle"}`)}}
	req := Request{UserID: "owner", ConversationID: "si_owner_peer", Keyword: "needle"}
	first, err := service.Search(context.Background(), "admin", req)
	if err != nil || len(first.Messages) != 0 || !first.HasMore || first.ScannedCount != 2000 {
		t.Fatal("bounded empty search page incorrectly completed", err)
	}
	req.Cursor = first.NextCursor
	next, err := service.Search(context.Background(), "admin", req)
	if err != nil || len(next.Messages) != 1 || next.HasMore || !next.SearchComplete {
		t.Fatal("continuation lost older keyword match", err)
	}
	ciphertext, key, _ := legacyVector(t)
	source.maximum = 1
	source.messages[1] = &sdkws.MsgData{Seq: 1, ContentType: constant.Text, Content: ciphertext, KeyVersion: 1}
	req.Cursor = ""
	req.Keyword = "synthetic"
	missing, err := service.Search(context.Background(), "admin", req)
	if err != nil || len(missing.Messages) != 0 || missing.UnsearchableCount != 1 || missing.SearchComplete {
		t.Fatal("missing decryption key falsely reported exhaustive search", err)
	}
	source.key = key
	decrypted, err := service.Search(context.Background(), "admin", req)
	if err != nil || len(decrypted.Messages) != 1 || decrypted.Messages[0]["decryptionState"] != "decrypted" {
		t.Fatal("native ciphertext keyword search failed", err)
	}
}
func TestPreviewConversationNameFilterPrecedesPagination(t *testing.T) {
	service, _, _ := fixtureService()
	result, err := service.Conversations(context.Background(), "admin", Request{UserID: "owner", Keyword: "gamma", PageSize: 1})
	if err != nil || result.Total != 1 || len(result.Conversations) != 1 || result.Conversations[0].ShowName != "Gamma" {
		t.Fatal("conversation keyword applied only to loaded page", err)
	}
}

func TestPreviewLargeMessagesHaveBoundedResponseWithoutSkipping(t *testing.T) {
	service, source, _ := fixtureService()
	source.maximum = 40
	source.messages = map[int64]*sdkws.MsgData{}
	for seq := int64(1); seq <= 40; seq++ {
		content, _ := json.Marshal(map[string]string{"content": strings.Repeat("x", 128*1024)})
		source.messages[seq] = &sdkws.MsgData{Seq: seq, ContentType: constant.Text, Content: content}
	}
	req := Request{UserID: "owner", ConversationID: "si_owner_peer", PageSize: 40}
	seen := map[int64]bool{}
	for i := 0; i < 10; i++ {
		page, err := service.Search(context.Background(), "admin", req)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded) > 4*1024*1024 {
			t.Fatal("preview response exceeds proxy limit")
		}
		for _, message := range page.Messages {
			seq := message["seq"].(int64)
			if seen[seq] {
				t.Fatal("large response cursor duplicated message")
			}
			seen[seq] = true
		}
		if !page.HasMore {
			break
		}
		req.Cursor = page.NextCursor
	}
	if len(seen) != 40 {
		t.Fatal("response size cursor skipped messages")
	}
}
