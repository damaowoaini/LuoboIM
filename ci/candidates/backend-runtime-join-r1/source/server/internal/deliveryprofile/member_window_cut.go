package deliveryprofile

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/mongo"
)

type memberCutRow struct {
	ID            string `bson:"_id"`
	Kind          string `bson:"kind"`
	GID           string `bson:"group_id"`
	Seed          string `bson:"seed_evidence_sha256"`
	Lower         int64  `bson:"lower"`
	Upper         int64  `bson:"upper"`
	GroupRevision int64  `bson:"group_revision"`
	Epoch         string `bson:"epoch"`
	CID           string `bson:"conversation_id"`
	UID           string `bson:"user_id"`
	Last          string `bson:"last_command"`
	Revision      int64  `bson:"revision"`
	Closed        bool   `bson:"closed"`
}

func memberRowCID(gid, cid string) bool {
	return gid != "" && (cid == msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, gid) || cid == msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, gid) || cid == msgprocessor.GetConversationIDBySessionType(constant.GroupChatType, gid))
}

// Preserve the already persisted v2 token recipe. Lower is a current
// monotonic authority bound, not a new token which silently revokes old cuts.
func memberCutToken(row memberCutRow) string { return Digest([]string{row.ID, row.Seed, row.Last}) }
func removalWindowToken(row memberCutRow) string {
	return Digest([]any{row.ID, row.Epoch, row.CID, row.GID, row.UID, row.Seed, row.Last, row.GroupRevision, row.Lower, row.Upper, row.Closed})
}

// A lower bound never widens a source cut. The original normal token must
// still match, and an independent n_ window must carry the SAME membership
// command. A notification CID is derived from the stored real group ID above.
func memberCutsAt(ctx context.Context, db *mongo.Database, epoch, normalCID, branchCID string, users []string, expected map[string]string, position int64, fence bool) (map[string]string, error) {
	if position < 1 {
		return nil, ErrInvalid
	}
	normal, err := memberCutRows(ctx, db, epoch, normalCID, users, fence)
	if err != nil {
		return nil, err
	}
	current := normal
	if branchCID != normalCID {
		current, err = memberCutRows(ctx, db, epoch, branchCID, users, fence)
		if err != nil {
			return nil, err
		}
	}
	result := map[string]string{}
	for _, uid := range users {
		original, actual := normal[uid], current[uid]
		token := memberCutToken(original)
		if original.Closed || actual.Closed || actual.GID != original.GID || actual.Last != original.Last || actual.GroupRevision != original.GroupRevision || position < actual.Lower || expected[SHA([]byte(uid))] != token {
			token = ""
		}
		result[uid] = token
	}
	return result, nil
}
