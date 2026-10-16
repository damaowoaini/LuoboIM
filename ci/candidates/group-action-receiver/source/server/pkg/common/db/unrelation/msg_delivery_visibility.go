package unrelation

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/mongo"
)

// The managed activation must use this constructor for EVERY MsgRPC/MsgTool
// writer. The ordinary constructor remains the legacy path, not a fallback
// from an activated but unavailable coordinator. Neither constructor does I/O.
func NewMsgMongoDriverWithDeliveryVisibility(db *mongo.Database, guard table.PendingDeliveryVisibility) (table.MsgDocModelInterface, error) {
	if db == nil || guard == nil {
		return nil, fmt.Errorf("pending delivery visibility unavailable")
	}
	result := NewMsgMongoDriver(db).(*MsgMongoDriver)
	result.deliveryVisibility = guard
	return result, nil
}

func (m *MsgMongoDriver) restrict(ctx context.Context, changes []table.PendingVisibilityChange, work func(context.Context) error) error {
	if m.deliveryVisibility == nil {
		return work(ctx)
	}
	return m.deliveryVisibility.MutateMessageVisibility(ctx, m.MsgCollection.Database(), changes, work)
}

// Parse the actual legacy doc_id suffix without guessing CID ownership. A CID
// may contain ':', so only its final canonical decimal block is interpreted.
func pendingDocumentChange(docID string, indexes []int) (table.PendingVisibilityChange, error) {
	split := strings.LastIndexByte(docID, ':')
	if split < 1 {
		return table.PendingVisibilityChange{}, fmt.Errorf("invalid message document")
	}
	block, err := strconv.ParseInt(docID[split+1:], 10, 64)
	if err != nil || block < 0 || strconv.FormatInt(block, 10) != docID[split+1:] || block > (math.MaxInt64-100)/100 {
		return table.PendingVisibilityChange{}, fmt.Errorf("invalid message block")
	}
	c := table.PendingVisibilityChange{ConversationID: docID[:split]}
	for _, index := range indexes {
		if index < 0 || index >= 100 {
			return c, fmt.Errorf("invalid message slot")
		}
		c.Sequences = append(c.Sequences, block*100+int64(index)+1)
	}
	return c, nil
}

func (m *MsgMongoDriver) DeleteMsgsInOneDocByIndex(ctx context.Context, docID string, indexes []int) error {
	if m.deliveryVisibility == nil {
		return m.rawDeleteMsgsInOneDocByIndex(ctx, docID, indexes)
	}
	if len(indexes) == 0 {
		return nil
	}
	c, err := pendingDocumentChange(docID, indexes)
	if err != nil {
		return err
	}
	return m.restrict(ctx, []table.PendingVisibilityChange{c}, func(tx context.Context) error { return m.rawDeleteMsgsInOneDocByIndex(tx, docID, indexes) })
}
func (m *MsgMongoDriver) DeleteDocs(ctx context.Context, docIDs []string) error {
	if m.deliveryVisibility == nil {
		return m.rawDeleteDocs(ctx, docIDs)
	}
	if len(docIDs) == 0 {
		return nil
	}
	var changes []table.PendingVisibilityChange
	indexes := make([]int, 100)
	for i := range indexes {
		indexes[i] = i
	}
	for _, id := range docIDs {
		c, err := pendingDocumentChange(id, indexes)
		if err != nil {
			return err
		}
		changes = append(changes, c)
	}
	return m.restrict(ctx, changes, func(tx context.Context) error { return m.rawDeleteDocs(tx, docIDs) })
}
func (m *MsgMongoDriver) PushUnique(ctx context.Context, docID string, index int64, key string, value any) (*mongo.UpdateResult, error) {
	if m.deliveryVisibility == nil || key != "del_list" {
		return m.rawPushUnique(ctx, docID, index, key, value)
	}
	owners, ok := value.([]string)
	if !ok || len(owners) == 0 || index < 0 || index >= 100 {
		return nil, fmt.Errorf("invalid personal deletion")
	}
	c, err := pendingDocumentChange(docID, []int{int(index)})
	if err != nil {
		return nil, err
	}
	c.Owners = append([]string(nil), owners...)
	var result *mongo.UpdateResult
	err = m.restrict(ctx, []table.PendingVisibilityChange{c}, func(tx context.Context) error {
		var e error
		result, e = m.rawPushUnique(tx, docID, index, key, value)
		return e
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (m *MsgMongoDriver) UpdateMsg(ctx context.Context, docID string, index int64, key string, value any) (*mongo.UpdateResult, error) {
	if m.deliveryVisibility == nil || key != "revoke" || value == nil {
		return m.rawUpdateMsg(ctx, docID, index, key, value)
	}
	if index < 0 || index >= 100 {
		return nil, fmt.Errorf("invalid revocation slot")
	}
	c, err := pendingDocumentChange(docID, []int{int(index)})
	if err != nil {
		return nil, err
	}
	var result *mongo.UpdateResult
	err = m.restrict(ctx, []table.PendingVisibilityChange{c}, func(tx context.Context) error {
		var e error
		result, e = m.rawUpdateMsg(tx, docID, index, key, value)
		return e
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (m *MsgMongoDriver) AdvanceMessageVisibility(ctx context.Context, floors []table.MessageVisibilityFloor) error {
	if m.deliveryVisibility == nil {
		return m.rawAdvanceMessageVisibility(ctx, floors)
	}
	if len(floors) == 0 {
		return nil
	}
	changes := make([]table.PendingVisibilityChange, 0, len(floors))
	for _, f := range floors {
		if f.ConversationID == "" || f.MinSeq < 0 {
			return fmt.Errorf("invalid message floor")
		}
		if f.MinSeq == 0 {
			continue
		}
		c := table.PendingVisibilityChange{ConversationID: f.ConversationID, Below: f.MinSeq, FloorOnly: table.IsRetentionFloorOnly(ctx)}
		if f.UserID != "" {
			c.Owners = []string{f.UserID}
		}
		changes = append(changes, c)
	}
	// A zero floor has no privacy restriction but still uses the strong writer.
	return m.restrict(ctx, changes, func(tx context.Context) error { return m.rawAdvanceMessageVisibility(tx, floors) })
}
