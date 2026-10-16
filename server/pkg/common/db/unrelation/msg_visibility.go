package unrelation

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Read only durable deletion metadata, including for bodies obtained from
// Redis. Absence is allowed for a newly cached message not persisted yet;
// an unavailable metadata store is an error, never implicit visibility.
func (m *MsgMongoDriver) HiddenMessageSequences(ctx context.Context, userID, conversationID string, seqs []int64) ([]int64, error) {
	docSeqs := m.model.GetDocIDSeqsMap(conversationID, seqs)
	ids := make([]string, 0, len(docSeqs))
	for id := range docSeqs {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	collection, err := m.MsgCollection.Clone(options.Collection().SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		return nil, err
	}
	cur, err := collection.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"doc_id": bson.M{"$in": ids}}},
		{"$project": bson.M{"_id": 0, "doc_id": 1, "msgs": bson.M{"$map": bson.M{"input": "$msgs", "as": "slot", "in": bson.M{
			"deleted": bson.M{"$ifNull": bson.A{"$$slot.global_deleted", false}},
			"hidden":  bson.M{"$in": bson.A{bson.M{"$literal": userID}, bson.M{"$ifNull": bson.A{"$$slot.del_list", bson.A{}}}}},
		}}}}},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	hidden := []int64{}
	for cur.Next(ctx) {
		var row struct {
			DocID string `bson:"doc_id"`
			Msgs  []struct {
				Deleted, Hidden bool
			} `bson:"msgs"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		for _, seq := range docSeqs[row.DocID] {
			index := m.model.GetMsgIndex(seq)
			if index >= 0 && index < int64(len(row.Msgs)) && (row.Msgs[index].Deleted || row.Msgs[index].Hidden) {
				hidden = append(hidden, seq)
			}
		}
	}
	return hidden, cur.Err()
}
