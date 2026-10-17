package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
)

type namespaceReaderFixture struct {
	windowReaderFixture
	allowed   map[int64]bool
	pages     [][]int64
	filterErr error
	foreign   bool
}

func (f *namespaceReaderFixture) ReadNamespaceHistoryUpper(ctx context.Context, cid, owner string) (bool, int64, error) {
	return f.ReadGroupHistoryUpper(ctx, cid, owner)
}
func (f *namespaceReaderFixture) FilterNamespaceHistorySequences(_ context.Context, cid, owner string, seqs []int64) (map[int64]bool, error) {
	f.cid, f.owner = cid, owner
	f.pages = append(f.pages, append([]int64(nil), seqs...))
	result := map[int64]bool{}
	for _, seq := range seqs {
		if f.allowed == nil || f.allowed[seq] {
			result[seq] = true
		}
	}
	if f.foreign {
		result[9999] = false
	}
	return result, f.filterErr
}

func TestManagedNotificationHistoryFiltersCachedSequenceGaps(t *testing.T) {
	for _, cid := range []string{"n_g", "n_owner_with_underscore_peer", "sg_g"} {
		t.Run(cid, func(t *testing.T) {
			c := &windowCacheFixture{}
			r := &namespaceReaderFixture{windowReaderFixture: windowReaderFixture{replies: []upperReply{{closed: true, upper: 100}}}, allowed: map[int64]bool{50: true, 100: true}}
			db := &commonMsgDatabase{cache: c, msgDocDatabase: &summaryMongoFixture{}, groupHistory: r}
			minimum, maximum, body, err := db.GetMsgBySeqs(context.Background(), "owner", cid, []int64{49, 50, 60, 70, 100})
			if err != nil || minimum != 50 || maximum != 100 || len(body) != 2 || body[0].Seq != 50 || body[1].Seq != 100 || !reflect.DeepEqual(c.asked, []int64{50, 60, 70, 100}) || r.cid != cid || r.owner != "owner" {
				t.Fatal("cached gap, namespace identity, or original floor escaped", err)
			}
		})
	}
}

func TestManagedHistoryFinalFailureAndForeignSequenceNeverReturnCacheBody(t *testing.T) {
	for _, name := range []string{"authority-failure", "foreign-key"} {
		t.Run(name, func(t *testing.T) {
			r := &namespaceReaderFixture{windowReaderFixture: windowReaderFixture{replies: []upperReply{{}}}}
			if name == "authority-failure" {
				r.filterErr = errors.New("synthetic exact-notice authority unavailable")
			} else {
				r.foreign = true
			}
			db := &commonMsgDatabase{cache: &windowCacheFixture{}, msgDocDatabase: &summaryMongoFixture{}, groupHistory: r}
			var body []*sdkws.MsgData
			_, _, body, err := db.GetMsgBySeqs(context.Background(), "owner", "n_g", []int64{100})
			if err == nil || len(body) != 0 {
				t.Fatal("failed/foreign authority returned cached body", err)
			}
		})
	}
}

func TestManagedHistorySequenceReadBudgetDoesNotLimitConversationCapacity(t *testing.T) {
	r := &namespaceReaderFixture{}
	db := &commonMsgDatabase{groupHistory: r}
	seqs := []int64{0, 1, 1}
	for i := int64(2); i <= 130; i++ {
		seqs = append(seqs, i)
	}
	allowed, managed, err := db.managedHistorySequences(context.Background(), "owner", "n_g", seqs)
	if err != nil || !managed || len(allowed) != 130 || len(r.pages) != 3 || len(r.pages[0]) != 64 || len(r.pages[1]) != 64 || len(r.pages[2]) != 2 || allowed[0] {
		t.Fatal("read budget became a product capacity cap or duplicated a sequence", err)
	}
}

func TestManagedHistoryInvalidReaderBindingCannotFallBackForNotification(t *testing.T) {
	r := selectGroupHistoryReader([]GroupHistoryWindowReader{&windowReaderFixture{}, &windowReaderFixture{}})
	db := &commonMsgDatabase{cache: &windowCacheFixture{}, msgDocDatabase: &summaryMongoFixture{}, groupHistory: r}
	if _, _, body, err := db.GetMsgBySeqs(context.Background(), "owner", "n_owner_peer", []int64{100}); err == nil || len(body) > 0 {
		t.Fatal("invalid required binding used legacy notification path", err)
	}
	if managed, allowed, err := db.ManagedNamespaceHistoryAccess(context.Background(), "owner", "n_owner_peer"); !managed || allowed || err == nil {
		t.Fatal("invalid managed binding became legacy mode")
	}
}
