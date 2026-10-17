package conversation_msg

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/constant"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/db_interface"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/model_struct"
)

// This optional port is implemented only by this independent Web overlay.
// Existing native SDK callback and database interfaces remain unchanged.
type ownReadTimeStore interface {
	CaptureOwnReadTimeScopeDB(context.Context, string) (string, error)
	ApplyOwnReadTimeDB(context.Context, string, string) (string, error)
}
type ownReadTimeListener interface{ OnOwnC2CReadTimeConfirmed(string) }

type ownReadTimeRow struct {
	ClientMsgID string `json:"clientMsgID"`
	Seq         int64  `json:"seq"`
	SendID      string `json:"sendID"`
	RecvID      string `json:"recvID"`
	ReadTime    int64  `json:"readTime,omitempty"`
}
type ownReadTimePacket struct {
	Version        int              `json:"version"`
	OwnerUserID    string           `json:"ownerUserID"`
	ConversationID string           `json:"conversationID"`
	PeerUserID     string           `json:"peerUserID"`
	SessionType    int32            `json:"sessionType"`
	ReceiptID      string           `json:"receiptID"`
	ReadTime       int64            `json:"readTime"`
	ReadScope      string           `json:"readScope"`
	Rows           []ownReadTimeRow `json:"rows"`
}

var errOwnReadTime = errors.New("own read time unavailable")

const minOwnReadTime = int64(946684800000)
const maxOwnReadTime = int64(4102444800000)
const maxOwnReadSeq = int64(9007199254740991)

// Constructed once before the first conversation lookup. A later clear,
// delete, account switch or DB replacement cannot renew this request's scope.
type ownReadTimeTicket struct {
	database       db_interface.DataBase
	owner          string
	conversationID string
	readScope      string
}

func (ticket *ownReadTimeTicket) current(ctx context.Context, c *Conversation) bool {
	return ticket != nil && ctx.Err() == nil && c.loginUserID == ticket.owner && c.db == ticket.database
}
func (c *Conversation) captureOwnReadTimeTicket(ctx context.Context, envelope *sdkws.MsgData,
	tips *sdkws.MarkAsReadTips) *ownReadTimeTicket {
	ticket := &ownReadTimeTicket{database: c.db, owner: c.loginUserID}
	if tips == nil {
		return ticket
	}
	ticket.conversationID = tips.ConversationID
	store, supported := ticket.database.(ownReadTimeStore)
	if !supported || !ticket.current(ctx, c) || envelope == nil || ticket.owner == "" ||
		tips.MarkAsReadUserID != ticket.owner || tips.ConversationID == "" ||
		envelope.SendID != ticket.owner || envelope.RecvID == "" || envelope.RecvID == ticket.owner ||
		envelope.SessionType != constant.SingleChatType || envelope.ContentType != constant.HasReadReceipt ||
		envelope.ClientMsgID == "" || envelope.SendTime < minOwnReadTime || envelope.SendTime > maxOwnReadTime {
		return ticket
	}
	readScope, err := store.CaptureOwnReadTimeScopeDB(ctx, ticket.conversationID)
	if err == nil && readScope != "" && ticket.current(ctx, c) {
		ticket.readScope = readScope
	}
	return ticket
}
func (c *Conversation) readDrawingConversation(ctx context.Context, envelope *sdkws.MsgData,
	tips *sdkws.MarkAsReadTips) (*model_struct.LocalConversation, *ownReadTimeTicket, error) {
	var ticket *ownReadTimeTicket
	database := c.db
	if tips.MarkAsReadUserID == c.loginUserID {
		ticket = c.captureOwnReadTimeTicket(ctx, envelope, tips)
		if !ticket.current(ctx, c) {
			return nil, ticket, errOwnReadTime
		}
		database = ticket.database
	}
	conversation, err := database.GetConversation(ctx, tips.ConversationID)
	if err != nil {
		return nil, ticket, err
	}
	if ticket != nil && !ticket.current(ctx, c) {
		return nil, ticket, errOwnReadTime
	}
	return conversation, ticket, nil
}

// The envelope here is the server HasReadReceipt notification, not the
// original chat message. No clock, original-message time or ACK time is used.
func ownReadTimeHeader(owner string, conversation *model_struct.LocalConversation,
	envelope *sdkws.MsgData, tips *sdkws.MarkAsReadTips) (ownReadTimePacket, map[int64]bool, error) {
	var empty ownReadTimePacket
	if owner == "" || conversation == nil || envelope == nil || tips == nil ||
		conversation.ConversationType != constant.SingleChatType ||
		conversation.ConversationID == "" || conversation.UserID == "" ||
		conversation.UserID == owner || tips.ConversationID != conversation.ConversationID ||
		tips.MarkAsReadUserID != owner || envelope.SendID != owner ||
		envelope.RecvID != conversation.UserID || envelope.SessionType != constant.SingleChatType ||
		envelope.ContentType != constant.HasReadReceipt || envelope.ClientMsgID == "" ||
		envelope.SendTime < minOwnReadTime || envelope.SendTime > maxOwnReadTime || len(tips.Seqs) == 0 {
		return empty, nil, errOwnReadTime
	}
	seqs := make(map[int64]bool, len(tips.Seqs))
	for _, seq := range tips.Seqs {
		if seq <= 0 || seq > maxOwnReadSeq || seqs[seq] {
			return empty, nil, errOwnReadTime
		}
		seqs[seq] = true
	}
	result := ownReadTimePacket{Version: 1, OwnerUserID: owner,
		ConversationID: conversation.ConversationID, PeerUserID: conversation.UserID,
		SessionType: constant.SingleChatType, ReceiptID: envelope.ClientMsgID,
		ReadTime: envelope.SendTime}
	return result, seqs, nil
}

func ownReadTimeInput(owner string, conversation *model_struct.LocalConversation,
	envelope *sdkws.MsgData, tips *sdkws.MarkAsReadTips,
	messages []*model_struct.LocalChatLog) (ownReadTimePacket, error) {
	result, seqs, err := ownReadTimeHeader(owner, conversation, envelope, tips)
	if err != nil {
		return ownReadTimePacket{}, err
	}
	var empty ownReadTimePacket
	ids, observedSeqs := map[string]bool{}, map[int64]bool{}
	for _, message := range messages {
		if message == nil || !seqs[message.Seq] || ids[message.ClientMsgID] || observedSeqs[message.Seq] ||
			message.ClientMsgID == "" || message.SessionType != constant.SingleChatType {
			return empty, errOwnReadTime
		}
		ids[message.ClientMsgID], observedSeqs[message.Seq] = true, true
		// Server conversation-read tips include both directions in the seq
		// interval. Validate every returned identity before skipping only the
		// exact owner's own outgoing row; foreign rows still reject the packet.
		if message.SendID == owner && message.RecvID == conversation.UserID {
			continue
		}
		if message.SendID != conversation.UserID || message.RecvID != owner {
			return empty, errOwnReadTime
		}
		result.Rows = append(result.Rows, ownReadTimeRow{ClientMsgID: message.ClientMsgID,
			Seq: message.Seq, SendID: message.SendID, RecvID: message.RecvID})
	}
	// Absent local rows are not guessed or backfilled. A later real replay may
	// resolve them; until then the UI retains its reversible safe hold.
	if len(result.Rows) == 0 {
		return empty, errOwnReadTime
	}
	return result, nil
}

func (c *Conversation) applyOwnReadTime(ctx context.Context, envelope *sdkws.MsgData,
	tips *sdkws.MarkAsReadTips, conversation *model_struct.LocalConversation, ticket *ownReadTimeTicket) {
	if !ticket.current(ctx, c) || ticket.readScope == "" || tips == nil || ticket.conversationID != tips.ConversationID {
		return
	}
	database := ticket.database
	store, supported := database.(ownReadTimeStore)
	listener, observable := c.msgListener().(ownReadTimeListener)
	if !supported || !observable || ctx.Err() != nil || conversation == nil || tips == nil {
		return
	}
	owner := ticket.owner
	if _, _, err := ownReadTimeHeader(owner, conversation, envelope, tips); err != nil {
		return
	}
	readScope := ticket.readScope
	messages, err := database.GetMessagesBySeqs(ctx, tips.ConversationID, tips.Seqs)
	if err != nil || !ticket.current(ctx, c) {
		return
	}
	request, err := ownReadTimeInput(owner, conversation, envelope, tips, messages)
	if err != nil {
		return
	}
	request.ReadScope = readScope
	encoded, err := json.Marshal(request)
	if err != nil {
		return
	}
	stored, err := store.ApplyOwnReadTimeDB(ctx, request.ConversationID, string(encoded))
	if err != nil || !ticket.current(ctx, c) {
		return
	}
	var receipt ownReadTimePacket
	if json.Unmarshal([]byte(stored), &receipt) != nil || receipt.Version != 1 ||
		receipt.OwnerUserID != owner || receipt.PeerUserID != request.PeerUserID ||
		receipt.ConversationID != request.ConversationID || receipt.ReceiptID != request.ReceiptID ||
		receipt.SessionType != constant.SingleChatType || receipt.ReadTime != request.ReadTime || receipt.ReadScope != request.ReadScope ||
		len(receipt.Rows) != len(request.Rows) {
		return
	}
	expected := make(map[string]ownReadTimeRow, len(request.Rows))
	for _, row := range request.Rows {
		expected[row.ClientMsgID] = row
	}
	seen := make(map[string]bool, len(receipt.Rows))
	for _, row := range receipt.Rows {
		original, ok := expected[row.ClientMsgID]
		if !ok || seen[row.ClientMsgID] || row.Seq != original.Seq ||
			row.SendID != original.SendID || row.RecvID != original.RecvID ||
			row.ReadTime <= 0 || row.ReadTime > request.ReadTime {
			return
		}
		seen[row.ClientMsgID] = true
	}
	// Only the durable bridge response reaches this distinct own-read event.
	// It never reuses peer receipt semantics for outgoing/sent messages.
	event := struct {
		SchemaVersion  int              `json:"schemaVersion"`
		OwnerID        string           `json:"ownerID"`
		PeerID         string           `json:"peerID"`
		ConversationID string           `json:"conversationID"`
		SessionType    int32            `json:"sessionType"`
		ReceiptID      string           `json:"receiptID"`
		CommittedRows  []ownReadTimeRow `json:"committedRows"`
	}{1, owner, request.PeerUserID, request.ConversationID,
		constant.SingleChatType, request.ReceiptID, receipt.Rows}
	encodedEvent, err := json.Marshal(event)
	if err == nil {
		listener.OnOwnC2CReadTimeConfirmed(string(encodedEvent))
	}
}
