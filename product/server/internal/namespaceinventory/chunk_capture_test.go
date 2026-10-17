package namespaceinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

type pagedFixture struct {
	id        Identity
	rows      map[string][]MongoRow
	keys      []RedisRow
	pageCalls int
}

func (s *pagedFixture) Identity(ctx context.Context) (Identity, error) {
	if e := ctx.Err(); e != nil {
		return Identity{}, e
	}
	return s.id, nil
}
func (s *pagedFixture) Page(ctx context.Context, c PageCursor, l Limits) (Page, error) {
	if e := ctx.Err(); e != nil {
		return Page{}, e
	}
	s.pageCalls++
	p := Page{Before: c, After: c}
	if c.Phase == "mongo" {
		name := collections[c.Collection]
		rows := s.rows[name]
		if !c.CatalogRead {
			var value interface{} = bson.D{{Key: "name", Value: name}, {Key: "observedAbsent", Value: true}}
			if len(rows) != 0 {
				value = bson.D{{Key: "name", Value: name}, {Key: "type", Value: "collection"}}
				p.After.CatalogRead = true
			} else {
				p.After = nextCollection(c)
			}
			raw, _ := bson.Marshal(value)
			p.Mongo = []MongoRow{{Collection: "@catalog", KeySHA: digest([]byte(name)), Raw: raw}}
			return p, nil
		}
		at := 0
		if len(c.AfterID) != 0 {
			at = int(bson.Raw(c.AfterID).Lookup("v").Int64())
		}
		end := at + l.Rows
		if end > len(rows) {
			end = len(rows)
		}
		p.Mongo = rows[at:end]
		if end == len(rows) {
			p.After = nextCollection(c)
		} else {
			p.After.AfterID, _ = bson.Marshal(bson.M{"v": int64(end)})
		}
		return p, nil
	}
	if c.Prefix == 0 && int(c.RedisCursor) < len(s.keys) {
		at := int(c.RedisCursor)
		p.Redis = []RedisRow{s.keys[at]}
		p.After.RedisCursor++
		if int(p.After.RedisCursor) == len(s.keys) {
			p.After = PageCursor{Phase: "redis", Prefix: 1}
		}
	} else if c.Prefix+1 == len(prefixes) {
		p.After = PageCursor{Phase: "done"}
	} else {
		p.After = PageCursor{Phase: "redis", Prefix: c.Prefix + 1}
	}
	return p, nil
}
func pageFixture(t *testing.T) *pagedFixture {
	t.Helper()
	normal := msgprocessor.NewOptions(msgprocessor.WithNotNotification(true))
	one := table.MsgDocModel{DocID: "si_a_b_c:0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "a_b", RecvID: "c", SessionType: constant.SingleChatType, Options: normal, Seq: 1}}, {}}}
	two := table.MsgDocModel{DocID: "si_a_b_c:0", Msg: []*table.MsgInfoModel{nil, {Msg: &table.MsgDataModel{SendID: "a", RecvID: "b_c", SessionType: constant.SingleChatType, Options: normal, Seq: 2}}}}
	notice := table.MsgDocModel{DocID: "n_a_b:0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "a", RecvID: "b", SessionType: constant.NotificationChatType, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false)), Seq: 1}}}}
	return &pagedFixture{id: fixtureIdentity(), rows: map[string][]MongoRow{"msg": {rawRow(t, "msg", "first-document", one), rawRow(t, "msg", "second-document", two), rawRow(t, "msg", "direct-notice", notice)}, "group": {rawRow(t, "group", "group", bson.M{"group_id": "a_b"})}}, keys: []RedisRow{{Key: "MAX_SEQ:n_a_b", Type: "string", Dump: []byte("synthetic-dump"), RawHead: "9", PTTL: -1}, {Key: "MAX_SEQ:n_a_b", Type: "string", Dump: []byte("synthetic-dump"), RawHead: "9", PTTL: -1}}}
}
func chunkFixtureLimits() Limits   { l := defaultPageLimits(); l.Rows = 1; return l }
func chunkDir(t *testing.T) string { t.Helper(); return filepath.Join(privateFixtureDir(t), "chunks") }
func finishChunks(t *testing.T, s *pagedFixture, dir string) ChunkSummary {
	t.Helper()
	var out ChunkSummary
	for n := 0; n < 128; n++ {
		var e error
		out, e = ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 3)
		if e != nil {
			t.Fatal("bounded resume failed", e)
		}
		if out.ObservedComplete {
			return out
		}
	}
	t.Fatal("bounded capture did not finish")
	return out
}
func firstPassOnly(t *testing.T, s *pagedFixture, dir string) {
	t.Helper()
	if _, e := BeginChunkCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), 1); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 64; n++ {
		metadata, e := loadSession(dir)
		if e != nil {
			t.Fatal(e)
		}
		p, e := replayJournal(context.Background(), dir, metadata, func(Page, int) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		if p.Pass == 2 {
			return
		}
		if _, e := ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 1); e != nil {
			t.Fatal(e)
		}
	}
	t.Fatal("first observation did not end")
}

func TestNamespaceChunksResumePreservesCrossPageEvidence(t *testing.T) {
	s, dir := pageFixture(t), chunkDir(t)
	partial, e := BeginChunkCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), 2)
	if e != nil || partial.ObservedComplete || partial.State != State {
		t.Fatal("initial bounded capture contract")
	}
	out := finishChunks(t, s, dir)
	if out.CandidateCollisions != 2 || out.ErasedSlots != 1 || out.RedisKeys != 1 || out.MongoRows != int64(len(collections)+4) || out.Reasons["historical-lifecycle-coverage-unverified"] != 1 {
		t.Fatal("cross-page collisions, erasure, duplicate SCAN or lifecycle evidence lost")
	}
	beforeCalls := s.pageCalls
	readOnly, e := ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 3)
	if e != nil || !reflect.DeepEqual(out, readOnly) || s.pageCalls != beforeCalls {
		t.Fatal("completed resume changed snapshot or repeated source work")
	}
	inspected, e := InspectChunks(context.Background(), dir)
	if e != nil || !reflect.DeepEqual(out, inspected) {
		t.Fatal("offline inspect failed to reconstruct committed raw evidence")
	}
	metadata, e := loadSession(dir)
	if e != nil {
		t.Fatal(e)
	}
	retained := map[string][]byte{}
	if _, e := replayJournal(context.Background(), dir, metadata, func(page Page, pass int) error {
		if pass == 1 {
			for _, row := range page.Mongo {
				if row.Collection != "@catalog" {
					retained[row.Collection+":"+row.KeySHA] = row.Raw
				}
			}
		}
		return nil
	}); e != nil {
		t.Fatal("raw journal replay failed")
	}
	for collection, rows := range s.rows {
		for _, row := range rows {
			if !bytes.Equal(retained[collection+":"+row.KeySHA], row.Raw) {
				t.Fatal("raw BSON/options/erased slot bytes changed across resume")
			}
		}
	}
	raw, _ := json.Marshal(out)
	for _, forbidden := range []string{"si_a_b_c", "n_a_b", "synthetic-tenant", "synthetic_db", "synthetic-dump"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("public summary disclosed private inventory")
		}
	}
}

func TestNamespaceChunksRejectObservedDriftAndRestart(t *testing.T) {
	for _, kind := range []string{"changed-row", "missing-row", "new-row", "changed-redis", "ttl-refreshed", "mongo-restart", "redis-restart", "binding-changed"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := pageFixture(t), chunkDir(t)
			firstPassOnly(t, s, dir)
			switch kind {
			case "changed-row":
				s.rows["group"][0] = rawRow(t, "group", "group", bson.M{"group_id": "changed"})
			case "missing-row":
				s.rows["msg"] = s.rows["msg"][:2]
			case "new-row":
				s.rows["group"] = append(s.rows["group"], rawRow(t, "group", "new", bson.M{"group_id": "new"}))
			case "changed-redis":
				s.keys[0].RawHead = "10"
			case "ttl-refreshed":
				s.keys[0].PTTL = 5000
			case "mongo-restart":
				s.id.MongoProcess = strings.Repeat("d", 24)
			case "redis-restart":
				s.id.Binding.RedisRunID = strings.Repeat("e", 40)
			case "binding-changed":
				s.id.Binding.Tenant = "another-tenant"
			}
			_, e := ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 128)
			if !errors.Is(e, ErrChanged) && !errors.Is(e, ErrIdentity) {
				t.Fatal("changed inventory/restart accepted", e)
			}
			if _, e := InspectChunks(context.Background(), dir); e != nil {
				t.Fatal("failed resume destroyed committed evidence", e)
			}
		})
	}
}

func TestNamespaceChunksCheckpointOwnershipAndLostReply(t *testing.T) {
	for _, kind := range []string{"lost-reply", "orphan-page", "partial-checkpoint", "tampered-page", "public-mode", "locked", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := pageFixture(t), chunkDir(t)
			if _, e := BeginChunkCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), 1); e != nil {
				t.Fatal(e)
			}
			session, e := loadSession(dir)
			if e != nil {
				t.Fatal(e)
			}
			position, e := replayJournal(context.Background(), dir, session, func(Page, int) error { return nil })
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "lost-reply":
				// Simulate a durable checkpoint whose CLI reply was never seen.
				before := s.pageCalls
				page, e := s.Page(context.Background(), position.Cursor, session.Limits)
				if e != nil {
					t.Fatal(e)
				}
				advanced, e := appendCheckpoint(dir, session, position, page)
				if e != nil {
					t.Fatal(e)
				}
				out, e := ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 1)
				if e != nil || out.Checkpoints != advanced.Sequence+1 || s.pageCalls != before+2 {
					t.Fatal("lost checkpoint reply was not recovered exactly once")
				}
				return
			case "orphan-page":
				orphan := filepath.Join(dir, "page-1cfad324-14cd-40bf-b688-2736144cbf32.json")
				if os.WriteFile(orphan, []byte("partial private orphan"), 0600) != nil {
					t.Fatal("orphan setup")
				}
				finishChunks(t, s, dir)
				if b, e := ReadPrivateDocument(orphan, 1024); e != nil || string(b) != "partial private orphan" {
					t.Fatal("uncommitted evidence was removed or trusted")
				}
				return
			case "partial-checkpoint":
				if os.WriteFile(filepath.Join(dir, checkpointName(position.Sequence+1)), []byte("{"), 0600) != nil {
					t.Fatal("partial setup")
				}
			case "tampered-page":
				var cp chunkCheckpoint
				if loadPrivateJSON(filepath.Join(dir, checkpointName(1)), 64<<10, &cp) != nil {
					t.Fatal("checkpoint setup")
				}
				p := filepath.Join(dir, cp.Page)
				b, _ := os.ReadFile(p)
				if os.WriteFile(p, append(b, ' '), 0600) != nil {
					t.Fatal("tamper setup")
				}
			case "public-mode":
				if os.Chmod(filepath.Join(dir, "session.json"), 0644) != nil {
					t.Fatal("mode setup")
				}
			case "locked":
				lock, e := lockJournal(dir)
				if e != nil {
					t.Fatal(e)
				}
				defer lock.Close()
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, e := ResumeChunkCapture(ctx, s, s.id.Binding, dir, 1); !errors.Is(e, context.Canceled) {
					t.Fatal("cancelled resume accepted")
				}
				return
			}
			if _, e := ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 1); e == nil {
				t.Fatal("partial/tampered/unowned/locked checkpoint accepted")
			}
		})
	}
}

func TestNamespaceChunksExceedFormerAggregateRowLimit(t *testing.T) {
	s := &pagedFixture{id: fixtureIdentity(), rows: map[string][]MongoRow{}}
	for n := 0; n < 10001; n++ {
		s.rows["group_member"] = append(s.rows["group_member"], rawRow(t, "group_member", fmt.Sprint(n), bson.M{"_id": n, "group_id": "synthetic-g", "user_id": "synthetic-u"}))
	}
	dir := chunkDir(t)
	out, e := BeginChunkCapture(context.Background(), s, s.id.Binding, dir, defaultPageLimits(), 128)
	if e != nil || !out.ObservedComplete || out.MongoRows != int64(10001+len(collections)) || out.State != State || out.Checkpoints <= 2 {
		t.Fatal("page limits still acted as a global history limit", e)
	}
}
