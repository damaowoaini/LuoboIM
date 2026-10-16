package unrelation

import (
	"context"
	"fmt"
	"regexp"

	"go.mongodb.org/mongo-driver/bson"
)

func (m *MsgMongoDriver) FindSeqsByClientMsgIDs(ctx context.Context, conversationID string, clientIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(clientIDs))
	if len(clientIDs) == 0 {
		return result, nil
	}
	// Include both scopes in the initial match. The projection returns only
	// matching metadata, not message bodies or other users' conversations.
	cur, err := m.MsgCollection.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"doc_id": bson.M{"$regex": "^" + regexp.QuoteMeta(conversationID) + ":[0-9]+$"}, "msgs.msg.client_msg_id": bson.M{"$in": clientIDs}}},
		{"$project": bson.M{"_id": 0, "matches": bson.M{"$filter": bson.M{"input": "$msgs", "as": "entry", "cond": bson.M{"$in": bson.A{"$$entry.msg.client_msg_id", clientIDs}}}}}},
		{"$unwind": "$matches"},
		{"$project": bson.M{"client_id": "$matches.msg.client_msg_id", "seq": "$matches.msg.seq"}},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			ClientID string `bson:"client_id"`
			Seq      int64  `bson:"seq"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		if row.Seq < 1 {
			return nil, fmt.Errorf("invalid persisted message identity")
		}
		if previous, ok := result[row.ClientID]; ok && previous != row.Seq {
			return nil, fmt.Errorf("ambiguous duplicate client message identity")
		}
		result[row.ClientID] = row.Seq
	}
	return result, cur.Err()
}
