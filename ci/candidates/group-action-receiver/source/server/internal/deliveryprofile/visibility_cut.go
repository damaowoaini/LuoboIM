package deliveryprofile

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"math"
	"sort"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

const cutCollection = "message_delivery_sequences_v1"

func cutID(epoch, cid, owner string) string {
	return Digest([]string{epoch, "owned-notification-cut", cid + "\x00" + owner})
}

type PrivacyScopeCut struct {
	ConversationID string           `bson:"conversation_id" json:"conversationID"`
	GlobalRevision int64            `bson:"global_revision" json:"globalRevision"`
	SourceHead     int64            `bson:"source_head" json:"sourceHead"`
	Owners         map[string]int64 `bson:"owners" json:"owners"`
}
type VisibilityCut struct {
	Version        string            `bson:"version" json:"version"`
	Notification   *PrivacyScopeCut  `bson:"notification" json:"notification"`
	ConversationID string            `bson:"conversation_id" json:"conversationID"`
	GlobalRevision int64             `bson:"global_revision" json:"globalRevision"`
	SourceHead     int64             `bson:"source_head" json:"sourceHead"`
	Owners         map[string]int64  `bson:"owners" json:"owners"`
	Members        map[string]string `bson:"members" json:"members"`
}
type cutRow struct {
	ID       string `bson:"_id"`
	Kind     string `bson:"kind"`
	Epoch    string `bson:"epoch"`
	CID      string `bson:"conversation_id"`
	Owner    string `bson:"owner"`
	Revision int64  `bson:"revision"`
	Fence    int64  `bson:"fence"`
	Below    int64  `bson:"below"`
}

func cutTX(ctx context.Context, db *mongo.Database, epoch, activation string) error {
	if ctx == nil || db == nil || !validID(epoch) || !validSHA(activation) {
		return ErrInvalid
	}
	session := mongo.SessionFromContext(ctx)
	x, ok := session.(mongo.XSession)
	if !ok || session.Client() != db.Client() {
		return ErrInvalid
	}
	tx := x.ClientSession()
	if tx == nil || !tx.TransactionRunning() || tx.CurrentRc == nil || tx.CurrentRc.Level != "snapshot" || tx.CurrentRp == nil || tx.CurrentRp.Mode() != readpref.PrimaryMode || tx.CurrentWc == nil || tx.CurrentWc.W != "majority" || tx.CurrentWc.Journal == nil || !*tx.CurrentWc.Journal {
		return ErrInvalid
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var marker struct {
		Epoch      string `bson:"epoch"`
		Activation string `bson:"activation_sha256"`
		Retired    bool   `bson:"legacy_writers_retired"`
		Version    int    `bson:"version"`
	}
	if db.Collection(cutCollection).FindOne(ctx, bson.M{"_id": "schema:message-delivery-v1"}).Decode(&marker) != nil || marker.Epoch != epoch || marker.Activation != activation || !marker.Retired || marker.Version != 1 {
		return ErrInvalid
	}
	return nil
}
func readCutRows(ctx context.Context, db *mongo.Database, epoch, cid string, owners []string) (map[string]cutRow, error) {
	ids := make([]string, 0, len(owners))
	wanted := map[string]string{}
	for _, owner := range owners {
		id := cutID(epoch, cid, owner)
		if _, ok := wanted[id]; ok {
			return nil, ErrInvalid
		}
		wanted[id] = owner
		ids = append(ids, id)
	}
	cursor, err := db.Collection(cutCollection).Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	result := map[string]cutRow{}
	for cursor.Next(ctx) {
		var raw bson.Raw
		decodeErr := cursor.Decode(&raw)
		var r cutRow
		shape := rawCutShape(raw, []string{"_id", "kind", "epoch", "conversation_id", "owner"}, []string{"revision", "fence", "below"}, nil)
		unmarshalErr := bson.Unmarshal(raw, &r)
		expectedOwner, exists := wanted[r.ID]
		if decodeErr != nil || !shape || unmarshalErr != nil || !exists || r.Kind != "owned-notification-cut-v1" || r.Epoch != epoch || r.CID != cid || r.ID != cutID(epoch, cid, r.Owner) || expectedOwner != r.Owner || r.Revision < 0 || r.Revision >= math.MaxInt64-1 || r.Fence < 1 || r.Fence >= math.MaxInt64-1 || r.Below < 0 {
			err = ErrInvalid
			break
		}
		if _, ok := result[r.Owner]; ok {
			err = ErrInvalid
			break
		}
		result[r.Owner] = r
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	return result, nil
}

// CaptureVisibilityCut freezes ALL authorized candidates in the producing
// business transaction. Each read also writes the exact same cut document, so
// a concurrent Clear cannot commit unnoticed via snapshot write-skew. Paging
// 64 is an I/O budget, not a group member/product capacity limit.
func capturePrivacyScope(ctx context.Context, db *mongo.Database, epoch, cid string, users []string) (PrivacyScopeCut, error) {
	head, err := freezeSourceHead(ctx, db, epoch, cid)
	if err != nil {
		return PrivacyScopeCut{}, err
	}
	all := append([]string{""}, users...)
	cut := PrivacyScopeCut{ConversationID: cid, SourceHead: head, Owners: map[string]int64{}}
	for start := 0; start < len(all); start += 64 {
		page := all[start:min(start+64, len(all))]
		rows, err := readCutRows(ctx, db, epoch, cid, page)
		if err != nil {
			return PrivacyScopeCut{}, err
		}
		writes := make([]mongo.WriteModel, 0, len(page))
		for _, owner := range page {
			r, ok := rows[owner]
			if owner == "" {
				cut.GlobalRevision = r.Revision
			} else {
				cut.Owners[SHA([]byte(owner))] = r.Revision
			}
			if !ok {
				writes = append(writes, mongo.NewInsertOneModel().SetDocument(cutRow{ID: cutID(epoch, cid, owner), Kind: "owned-notification-cut-v1", Epoch: epoch, CID: cid, Owner: owner, Fence: 1}))
			} else {
				writes = append(writes, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": r.ID, "revision": r.Revision, "fence": r.Fence}).SetUpdate(bson.M{"$inc": bson.M{"fence": int64(1)}}))
			}
		}
		result, err := db.Collection(cutCollection).BulkWrite(ctx, writes)
		if err != nil {
			return PrivacyScopeCut{}, err
		}
		if result.InsertedCount+result.MatchedCount != int64(len(page)) {
			return PrivacyScopeCut{}, ErrInvalid
		}
	}
	return cut, nil
}
func CaptureVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation, cid string, users []string) (VisibilityCut, error) {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return VisibilityCut{}, err
	}
	if cid == "" || msgprocessor.IsNotification(cid) {
		return VisibilityCut{}, ErrInvalid
	}
	sorted := append([]string(nil), users...)
	sort.Strings(sorted)
	for i, uid := range sorted {
		if uid == "" || i > 0 && uid == sorted[i-1] {
			return VisibilityCut{}, ErrInvalid
		}
	}
	chat, err := capturePrivacyScope(ctx, db, epoch, cid, sorted)
	if err != nil {
		return VisibilityCut{}, err
	}
	notification, err := capturePrivacyScope(ctx, db, epoch, "n_"+cid, sorted)
	if err != nil {
		return VisibilityCut{}, err
	}
	cut := VisibilityCut{Version: "owned-visibility-cut-v2", ConversationID: cid, GlobalRevision: chat.GlobalRevision, SourceHead: chat.SourceHead, Owners: chat.Owners, Members: map[string]string{}, Notification: &notification}
	for start := 0; start < len(sorted); start += 64 {
		page := sorted[start:min(start+64, len(sorted))]
		members, err := memberCuts(ctx, db, epoch, cid, page, nil, true)
		if err != nil {
			return VisibilityCut{}, err
		}
		for uid, command := range members {
			cut.Members[SHA([]byte(uid))] = command
		}
	}
	return cut, nil
}
func comparePrivacyScope(ctx context.Context, db *mongo.Database, epoch string, cut PrivacyScopeCut, users []string, position int64, fence bool) (map[string]bool, error) {
	if cut.SourceHead < 0 || cut.SourceHead >= math.MaxInt64-1 || position < 0 {
		return nil, ErrInvalid
	}
	if position == 0 {
		position = cut.SourceHead + 1
	}
	all := append([]string{""}, users...)
	rows, err := readCutRows(ctx, db, epoch, cut.ConversationID, all)
	if err != nil {
		return nil, err
	}
	global := rows[""].Revision != cut.GlobalRevision || rows[""].Below > position
	denied := map[string]bool{}
	writes := make([]mongo.WriteModel, 0, len(all))
	for _, owner := range all {
		r, ok := rows[owner]
		if !ok {
			return nil, ErrInvalid
		}
		if owner != "" {
			revision, exists := cut.Owners[SHA([]byte(owner))]
			if !exists || revision < 0 {
				return nil, ErrInvalid
			}
			denied[owner] = global || revision != r.Revision || r.Below > position
		}
		if fence {
			writes = append(writes, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": r.ID, "revision": r.Revision, "fence": r.Fence}).SetUpdate(bson.M{"$inc": bson.M{"fence": int64(1)}}))
		}
	}
	if fence {
		result, err := db.Collection(cutCollection).BulkWrite(ctx, writes)
		if err != nil {
			return nil, err
		}
		if result.MatchedCount != int64(len(all)) {
			return nil, ErrInvalid
		}
	}
	return denied, nil
}

// A chat Clear narrows both projections. A notification-only n_ Clear narrows
// only that Seq0 branch, not a still-authorized stored chat copy. Membership
// always uses the original normal CID window, never an invented n_ window.
func CompareBranchVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation string, cut VisibilityCut, branchCID string, users []string, sequence, normalSequence int64, fence bool) (map[string]bool, error) {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return nil, err
	}
	if cut.Version != "owned-visibility-cut-v2" || cut.ConversationID == "" || msgprocessor.IsNotification(cut.ConversationID) || cut.GlobalRevision < 0 || cut.Notification == nil || cut.Notification.ConversationID != "n_"+cut.ConversationID || cut.Notification.GlobalRevision < 0 || cut.Notification.Owners == nil || cut.Members == nil || len(users) > 64 {
		return nil, ErrInvalid
	}
	if !cut.Valid() || sequence < 0 || sequence == math.MaxInt64 || normalSequence < 0 || normalSequence == math.MaxInt64 || branchCID != cut.ConversationID && branchCID != cut.Notification.ConversationID {
		return nil, ErrInvalid
	}
	chatPosition := normalSequence
	if branchCID == cut.ConversationID {
		if normalSequence != 0 && normalSequence != sequence {
			return nil, ErrInvalid
		}
		chatPosition = sequence
	}
	denied, err := comparePrivacyScope(ctx, db, epoch, PrivacyScopeCut{ConversationID: cut.ConversationID, GlobalRevision: cut.GlobalRevision, SourceHead: cut.SourceHead, Owners: cut.Owners}, users, chatPosition, fence)
	if err != nil {
		return nil, err
	}
	if branchCID == cut.Notification.ConversationID {
		silent, err := comparePrivacyScope(ctx, db, epoch, *cut.Notification, users, sequence, fence)
		if err != nil {
			return nil, err
		}
		for uid, value := range silent {
			denied[uid] = denied[uid] || value
		}
	}
	members, err := memberCuts(ctx, db, epoch, cut.ConversationID, users, cut.Members, fence)
	if err != nil {
		return nil, err
	}
	for _, uid := range users {
		denied[uid] = denied[uid] || members[uid] == ""
	}
	return denied, nil
}

// BumpVisibilityCut is called ONLY by the existing authenticated visibility
// mutation adapter, in the SAME TX as the source floor/Clear and duty erasure.
// Absence is initialized conservatively; it never rewinds a privacy generation.
func BumpVisibilityCut(ctx context.Context, db *mongo.Database, epoch, activation, cid string, owners []string, below int64, floorOnly bool) error {
	if err := cutTX(ctx, db, epoch, activation); err != nil {
		return err
	}
	if below < 1 {
		return ErrInvalid
	}
	if len(owners) == 0 {
		owners = []string{""}
	}
	for start := 0; start < len(owners); start += 64 {
		page := owners[start:min(start+64, len(owners))]
		rows, err := readCutRows(ctx, db, epoch, cid, page)
		if err != nil {
			return err
		}
		writes := make([]mongo.WriteModel, 0, len(page))
		for _, owner := range page {
			r, exists := rows[owner]
			revision := r.Revision
			if !floorOnly {
				revision++
			}
			minimum := max(r.Below, below)
			if !exists {
				writes = append(writes, mongo.NewInsertOneModel().SetDocument(cutRow{ID: cutID(epoch, cid, owner), Kind: "owned-notification-cut-v1", Epoch: epoch, CID: cid, Owner: owner, Revision: revision, Fence: 1, Below: minimum}))
			} else {
				writes = append(writes, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": r.ID, "revision": r.Revision, "fence": r.Fence}).SetUpdate(bson.M{"$set": bson.M{"revision": revision, "below": minimum}, "$inc": bson.M{"fence": int64(1)}}))
			}
		}
		result, err := db.Collection(cutCollection).BulkWrite(ctx, writes)
		if err != nil {
			return err
		}
		if result.InsertedCount+result.MatchedCount != int64(len(page)) {
			return ErrInvalid
		}
	}
	return nil
}

// Returns the immutable command token for an admitted original member. A new
// rejoin command cannot inherit the prior notification. Caller pages<=64.
func memberCuts(ctx context.Context, db *mongo.Database, epoch, cid string, users []string, expected map[string]string, fence bool) (map[string]string, error) {
	if len(users) > 64 {
		return nil, ErrInvalid
	}
	result := map[string]string{}
	if len(users) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(users))
	wanted := map[string]string{}
	for _, uid := range users {
		id := Digest([]string{epoch, "membership-window", cid + "\x00" + uid})
		if _, ok := wanted[id]; ok {
			return nil, ErrInvalid
		}
		wanted[id] = uid
		ids = append(ids, id)
	}
	cursor, err := db.Collection(cutCollection).Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	writes := []mongo.WriteModel{}
	seen := map[string]bool{}
	for cursor.Next(ctx) {
		var row struct {
			ID            string `bson:"_id"`
			Kind          string `bson:"kind"`
			GID           string `bson:"group_id"`
			Seed          string `bson:"seed_evidence_sha256"`
			Upper         int64  `bson:"upper"`
			GroupRevision int64  `bson:"group_revision"`
			Epoch         string `bson:"epoch"`
			CID           string `bson:"conversation_id"`
			UID           string `bson:"user_id"`
			Last          string `bson:"last_command"`
			Revision      int64  `bson:"revision"`
			Closed        bool   `bson:"closed"`
		}
		var raw bson.Raw
		decodeErr := cursor.Decode(&raw)
		shape := rawCutShape(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"upper", "revision", "group_revision"}, []string{"closed"})
		unmarshalErr := bson.Unmarshal(raw, &row)
		if decodeErr != nil || !shape || unmarshalErr != nil || wanted[row.ID] != row.UID || row.Epoch != epoch || row.CID != cid || row.UID == "" || row.Kind != "membership-window-v1" || msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, row.GID) != cid || !validSHA(row.Seed) || row.Upper < 0 || row.GroupRevision < 1 || row.GroupRevision >= math.MaxInt64 || row.Last != "" && !validID(row.Last) || row.Closed && row.Last == "" || !row.Closed && row.Upper != 0 || row.Revision < 1 || row.Revision >= math.MaxInt64-1 || seen[row.UID] {
			err = ErrInvalid
			break
		}
		seen[row.UID] = true
		token := Digest([]string{row.ID, row.Seed, row.Last})
		if row.Closed || expected != nil && expected[SHA([]byte(row.UID))] != token {
			token = ""
		}
		if expected == nil && token == "" {
			err = ErrInvalid
			break
		}
		result[row.UID] = token
		if fence {
			writes = append(writes, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": row.ID, "revision": row.Revision, "last_command": row.Last, "closed": row.Closed}).SetUpdate(bson.M{"$inc": bson.M{"revision": int64(1)}}))
		}
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if len(seen) != len(users) {
		return nil, ErrInvalid
	}
	if fence {
		r, err := db.Collection(cutCollection).BulkWrite(ctx, writes)
		if err != nil {
			return nil, err
		}
		if r.MatchedCount != int64(len(users)) {
			return nil, ErrInvalid
		}
	}
	return result, nil
}

func rawCutShape(raw bson.Raw, strings, longs, bools []string) bool {
	fields, err := raw.Elements()
	if err != nil || len(fields) != len(strings)+len(longs)+len(bools) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key()] {
			return false
		}
		seen[f.Key()] = true
	}
	for _, name := range strings {
		if _, ok := raw.Lookup(name).StringValueOK(); !ok {
			return false
		}
	}
	for _, name := range longs {
		if _, ok := raw.Lookup(name).Int64OK(); !ok {
			return false
		}
	}
	for _, name := range bools {
		if _, ok := raw.Lookup(name).BooleanOK(); !ok {
			return false
		}
	}
	return true
}

// Valid validates only the frozen shape; it does not create or renew a cut.
func (cut VisibilityCut) Valid() bool {
	if cut.Version != "owned-visibility-cut-v2" || cut.ConversationID == "" || msgprocessor.IsNotification(cut.ConversationID) || cut.GlobalRevision < 0 || cut.GlobalRevision >= math.MaxInt64-1 || cut.SourceHead < 0 || cut.SourceHead >= math.MaxInt64-1 || cut.Owners == nil || cut.Members == nil || cut.Notification == nil || cut.Notification.ConversationID != "n_"+cut.ConversationID || cut.Notification.GlobalRevision < 0 || cut.Notification.GlobalRevision >= math.MaxInt64-1 || cut.Notification.SourceHead < 0 || cut.Notification.SourceHead >= math.MaxInt64-1 || cut.Notification.Owners == nil || len(cut.Owners) != len(cut.Members) || len(cut.Owners) != len(cut.Notification.Owners) {
		return false
	}
	for uid, revision := range cut.Owners {
		other, ok := cut.Notification.Owners[uid]
		if !validSHA(uid) || revision < 0 || revision >= math.MaxInt64-1 || !ok || other < 0 || other >= math.MaxInt64-1 || !validSHA(cut.Members[uid]) {
			return false
		}
	}
	return true
}

// Freeze the shared allocator head and CAS its exact revision in the same
// producing TX. A competing ordinary allocation forces a transaction retry.
// Both normal and n_ counters need an explicit prepared seed, including Seq0
// configurations; absence never becomes a guessed zero. head+1 cannot wrap.
func freezeSourceHead(ctx context.Context, db *mongo.Database, epoch, cid string) (int64, error) {
	var row struct {
		ID          string `bson:"_id"`
		Epoch       string `bson:"epoch"`
		CID         string `bson:"conversation_id"`
		Maximum     int64  `bson:"maximum"`
		Revision    int64  `bson:"revision"`
		SeedMaximum int64  `bson:"seed_maximum"`
		SeedSHA     string `bson:"seed_evidence_sha256"`
	}
	id := Digest([]string{epoch, "sequence", cid})
	var raw bson.Raw
	if err := db.Collection(cutCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
		return 0, err
	}
	if !rawCutShape(raw, []string{"_id", "epoch", "conversation_id", "seed_evidence_sha256"}, []string{"maximum", "revision", "seed_maximum"}, nil) || bson.Unmarshal(raw, &row) != nil || row.ID != id || row.Epoch != epoch || row.CID != cid || row.Maximum < 0 || row.Maximum >= math.MaxInt64-1 || row.SeedMaximum < 0 || row.Maximum < row.SeedMaximum || !validSHA(row.SeedSHA) || row.Revision < 1 || row.Revision >= math.MaxInt64-1 {
		return 0, ErrInvalid
	}
	changed, err := db.Collection(cutCollection).UpdateOne(ctx, bson.M{"_id": id, "revision": row.Revision, "maximum": row.Maximum}, bson.M{"$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return 0, err
	}
	if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
		return 0, ErrInvalid
	}
	return row.Maximum, nil
}

// Strict persisted shape prevents a pre-head legacy cut (or missing BSON long)
// from silently decoding to head=0 and gaining a new restoration lease.
func (cut *VisibilityCut) UnmarshalBSON(raw []byte) error {
	document := bson.Raw(raw)
	fields, err := document.Elements()
	if err != nil || len(fields) != 7 {
		return ErrInvalid
	}
	wanted := map[string]bool{"version": true, "conversation_id": true, "global_revision": true, "source_head": true, "owners": true, "members": true, "notification": true}
	for _, field := range fields {
		if !wanted[field.Key()] {
			return ErrInvalid
		}
		delete(wanted, field.Key())
	}
	if len(wanted) != 0 {
		return ErrInvalid
	}
	for _, key := range []string{"version", "conversation_id"} {
		if _, ok := document.Lookup(key).StringValueOK(); !ok {
			return ErrInvalid
		}
	}
	for _, key := range []string{"global_revision", "source_head"} {
		if _, ok := document.Lookup(key).Int64OK(); !ok {
			return ErrInvalid
		}
	}
	notification, ok := document.Lookup("notification").DocumentOK()
	if !ok {
		return ErrInvalid
	}
	parts, err := notification.Elements()
	if err != nil || len(parts) != 4 {
		return ErrInvalid
	}
	expected := map[string]bool{"conversation_id": true, "global_revision": true, "source_head": true, "owners": true}
	for _, field := range parts {
		if !expected[field.Key()] {
			return ErrInvalid
		}
		delete(expected, field.Key())
	}
	if len(expected) != 0 {
		return ErrInvalid
	}
	if _, ok := notification.Lookup("conversation_id").StringValueOK(); !ok {
		return ErrInvalid
	}
	for _, key := range []string{"global_revision", "source_head"} {
		if _, ok := notification.Lookup(key).Int64OK(); !ok {
			return ErrInvalid
		}
	}
	for _, item := range []struct {
		source bson.Raw
		key    string
		text   bool
	}{{document, "owners", false}, {document, "members", true}, {notification, "owners", false}} {
		values, ok := item.source.Lookup(item.key).DocumentOK()
		if !ok {
			return ErrInvalid
		}
		rows, err := values.Elements()
		if err != nil {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, row := range rows {
			if !validSHA(row.Key()) || seen[row.Key()] {
				return ErrInvalid
			}
			seen[row.Key()] = true
			if item.text {
				if _, ok := row.Value().StringValueOK(); !ok {
					return ErrInvalid
				}
			} else {
				if _, ok := row.Value().Int64OK(); !ok {
					return ErrInvalid
				}
			}
		}
	}
	type plain VisibilityCut
	var decoded plain
	if bson.Unmarshal(raw, &decoded) != nil {
		return ErrInvalid
	}
	*cut = VisibilityCut(decoded)
	if !cut.Valid() {
		return ErrInvalid
	}
	return nil
}
