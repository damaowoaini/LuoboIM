package conversation_msg

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk_callback"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/constant"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/db_interface"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/model_struct"
	"testing"
	"time"
)

func ownReadFixture() (*model_struct.LocalConversation, *sdkws.MsgData, *sdkws.MarkAsReadTips, []*model_struct.LocalChatLog) {
	return &model_struct.LocalConversation{ConversationID: "si_fixture-owner_fixture-peer", ConversationType: 1, UserID: "fixture-peer"},
		&sdkws.MsgData{SendID: "fixture-owner", RecvID: "fixture-peer", SessionType: 1, ContentType: constant.HasReadReceipt, ClientMsgID: "server-read-receipt", SendTime: 1700000000123},
		&sdkws.MarkAsReadTips{MarkAsReadUserID: "fixture-owner", ConversationID: "si_fixture-owner_fixture-peer", Seqs: []int64{7}},
		[]*model_struct.LocalChatLog{{ClientMsgID: "incoming-id", Seq: 7, SendID: "fixture-peer", RecvID: "fixture-owner", SessionType: 1, SendTime: 123}}
}
func TestOwnReadTimeEnvelopeAndIncomingIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model_struct.LocalConversation, *sdkws.MsgData, *sdkws.MarkAsReadTips, []*model_struct.LocalChatLog)
	}{
		{"foreign actor", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			p.MarkAsReadUserID = "foreign"
		}},
		{"foreign CID", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			p.ConversationID = "si_foreign"
		}},
		{"foreign peer", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			e.RecvID = "foreign"
		}},
		{"ordinary message envelope", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			e.ContentType = 101
		}},
		{"missing authoritative time", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			e.SendTime = 0
		}},
		{"duplicate seq", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			p.Seqs = append(p.Seqs, 7)
		}},
		{"outgoing row", func(c *model_struct.LocalConversation, e *sdkws.MsgData, p *sdkws.MarkAsReadTips, r []*model_struct.LocalChatLog) {
			r[0].SendID = "fixture-owner"
			r[0].RecvID = "fixture-peer"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, e, p, r := ownReadFixture()
			test.mutate(c, e, p, r)
			if _, err := ownReadTimeInput("fixture-owner", c, e, p, r); err == nil {
				t.Fatal("unbound own-read evidence accepted")
			}
		})
	}
	t.Run("mixed directions keep only exact incoming rows", func(t *testing.T) {
		c, e, p, r := ownReadFixture()
		p.Seqs = append(p.Seqs, 8)
		r = append(r, &model_struct.LocalChatLog{ClientMsgID: "outgoing-id", Seq: 8, SendID: "fixture-owner", RecvID: "fixture-peer", SessionType: 1})
		got, err := ownReadTimeInput("fixture-owner", c, e, p, r)
		if err != nil || len(got.Rows) != 1 || got.Rows[0].ClientMsgID != "incoming-id" || got.Rows[0].Seq != 7 {
			t.Fatal("legitimate outgoing seq rejected incoming receipt or became own-read row")
		}
	})
	t.Run("mixed directions reject forged outgoing identity", func(t *testing.T) {
		c, e, p, r := ownReadFixture()
		p.Seqs = append(p.Seqs, 8)
		r = append(r, &model_struct.LocalChatLog{ClientMsgID: "outgoing-id", Seq: 8, SendID: "fixture-owner", RecvID: "foreign-peer", SessionType: 1})
		if _, err := ownReadTimeInput("fixture-owner", c, e, p, r); err == nil {
			t.Fatal("foreign outgoing identity accepted")
		}
	})
	t.Run("mixed directions reject duplicate ID and seq before skipping", func(t *testing.T) {
		for _, kind := range []string{"ID", "seq"} {
			c, e, p, r := ownReadFixture()
			p.Seqs = append(p.Seqs, 8)
			outgoing := &model_struct.LocalChatLog{ClientMsgID: "outgoing-id", Seq: 8, SendID: "fixture-owner", RecvID: "fixture-peer", SessionType: 1}
			if kind == "ID" {
				outgoing.ClientMsgID = r[0].ClientMsgID
			} else {
				outgoing.Seq = r[0].Seq
			}
			r = append(r, outgoing)
			if _, err := ownReadTimeInput("fixture-owner", c, e, p, r); err == nil {
				t.Fatal("duplicate outgoing identity skipped before validation", kind)
			}
		}
	})
	t.Run("real envelope time ignores original send time", func(t *testing.T) {
		c, e, p, r := ownReadFixture()
		got, err := ownReadTimeInput("fixture-owner", c, e, p, r)
		if err != nil || got.ReadTime != e.SendTime || got.ReadTime == r[0].SendTime || got.Rows[0].ClientMsgID != r[0].ClientMsgID {
			t.Fatal("receipt timestamp or exact identity lost")
		}
	})
}

type ownReadFakeDB struct {
	db_interface.DataBase
	rows             []*model_struct.LocalChatLog
	lookups, writes  int
	scopeCaptures    int
	scope            string
	conversation     *model_struct.LocalConversation
	conversationGate func()
	fail             bool
	change           bool
}

func (f *ownReadFakeDB) scopeValue() string {
	if f.scope != "" {
		return f.scope
	}
	return "fixture-history-turn"
}
func (f *ownReadFakeDB) CaptureOwnReadTimeScopeDB(context.Context, string) (string, error) {
	f.scopeCaptures++
	return f.scopeValue(), nil
}
func (f *ownReadFakeDB) GetConversation(context.Context, string) (*model_struct.LocalConversation, error) {
	if f.conversationGate != nil {
		f.conversationGate()
	}
	return f.conversation, nil
}
func (f *ownReadFakeDB) GetMessagesBySeqs(context.Context, string, []int64) ([]*model_struct.LocalChatLog, error) {
	f.lookups++
	return f.rows, nil
}
func (f *ownReadFakeDB) ApplyOwnReadTimeDB(_ context.Context, _ string, encoded string) (string, error) {
	f.writes++
	if f.fail {
		return "", errors.New("unconfirmed durability")
	}
	var packet ownReadTimePacket
	if err := json.Unmarshal([]byte(encoded), &packet); err != nil {
		return "", err
	}
	if packet.ReadScope != f.scopeValue() {
		return "", errors.New("history scope changed")
	}
	for i := range packet.Rows {
		packet.Rows[i].ReadTime = packet.ReadTime - 1
	}
	if f.change {
		packet.Rows[0].Seq++
	}
	b, err := json.Marshal(packet)
	return string(b), err
}

type ownReadFakeListener struct {
	open_im_sdk_callback.OnAdvancedMsgListener
	events []string
}

func (l *ownReadFakeListener) OnOwnC2CReadTimeConfirmed(value string) {
	l.events = append(l.events, value)
}
func TestOwnReadTimeCoreDurablePortAndDistinctEvent(t *testing.T) {
	for _, mode := range []string{"committed", "unconfirmed", "wrong stored identity", "foreign preflight"} {
		t.Run(mode, func(t *testing.T) {
			conversation, envelope, tips, rows := ownReadFixture()
			database := &ownReadFakeDB{rows: rows, fail: mode == "unconfirmed", change: mode == "wrong stored identity"}
			listener := &ownReadFakeListener{}
			c := &Conversation{db: database, loginUserID: "fixture-owner", msgListener: func() open_im_sdk_callback.OnAdvancedMsgListener { return listener }}
			if mode == "foreign preflight" {
				envelope.SendID = "foreign"
			}
			ticket := c.captureOwnReadTimeTicket(context.Background(), envelope, tips)
			c.applyOwnReadTime(context.Background(), envelope, tips, conversation, ticket)
			if mode == "foreign preflight" && (database.lookups != 0 || database.writes != 0) {
				t.Fatal("foreign evidence reached storage")
			}
			if mode != "committed" {
				if len(listener.events) != 0 {
					t.Fatal("unconfirmed or foreign result emitted own-read event")
				}
				return
			}
			if database.writes != 1 || len(listener.events) != 1 {
				t.Fatal("durable port/event ordering failed")
			}
			var event struct {
				SchemaVersion int              `json:"schemaVersion"`
				OwnerID       string           `json:"ownerID"`
				PeerID        string           `json:"peerID"`
				CommittedRows []ownReadTimeRow `json:"committedRows"`
			}
			if json.Unmarshal([]byte(listener.events[0]), &event) != nil || event.SchemaVersion != 1 || event.OwnerID != "fixture-owner" || event.PeerID != "fixture-peer" || len(event.CommittedRows) != 1 || event.CommittedRows[0].ReadTime != envelope.SendTime-1 {
				t.Fatal("exact persisted own-time DTO lost")
			}
		})
	}
	t.Run("scope is captured before delayed conversation lookup", func(t *testing.T) {
		ownReadDelayedConversation(t, "unchanged")
	})
	t.Run("delayed conversation cannot renew history or owner scope", func(t *testing.T) {
		for _, kind := range []string{"history-clear", "owner", "database"} {
			ownReadDelayedConversation(t, kind)
		}
	})

}

func ownReadDelayedConversation(t *testing.T, kind string) {
	t.Helper()
	conversation, envelope, tips, rows := ownReadFixture()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	database := &ownReadFakeDB{rows: rows, conversation: conversation}
	database.conversationGate = func() { close(entered); <-release }
	listener := &ownReadFakeListener{}
	c := &Conversation{db: database, loginUserID: "fixture-owner", msgListener: func() open_im_sdk_callback.OnAdvancedMsgListener { return listener }}
	go func() {
		resolved, ticket, err := c.readDrawingConversation(context.Background(), envelope, tips)
		if err == nil {
			c.applyOwnReadTime(context.Background(), envelope, tips, resolved, ticket)
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actual GetConversation gate was not reached", kind)
	}
	if database.scopeCaptures != 1 {
		t.Fatal("scope not captured exactly once before first conversation lookup", kind)
	}
	switch kind {
	case "history-clear":
		database.scope = "new-history-after-delete-recreate"
	case "owner":
		c.loginUserID = "new-owner"
	case "database":
		c.db = &ownReadFakeDB{rows: rows, conversation: conversation}
	}
	close(release)
	released = true
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded conversation completion missing", kind)
	}
	if database.scopeCaptures != 1 {
		t.Fatal("old operation renewed its history ticket", kind)
	}
	if kind == "unchanged" {
		if database.lookups != 1 || database.writes != 1 || len(listener.events) != 1 {
			t.Fatal("unchanged delayed scope failed", kind)
		}
	} else if len(listener.events) != 0 {
		t.Fatal("retired history or owner emitted own-read event", kind)
	}
	if (kind == "owner" || kind == "database") && (database.lookups != 0 || database.writes != 0) {
		t.Fatal("retired owner reached later DB port", kind)
	}
}
