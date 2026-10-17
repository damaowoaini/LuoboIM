//go:build linux

package messagedelivery

import (
	"context"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// Custody/caller-binding control only. The borrowed real strict archive codec
// uses synthetic preimages here, NOT a publication, retirement/Ready capability
// or a successful old-history import. No full-chain Mongo result is claimed.
func TestOriginalHistoryVerifierSelectsExactArchivedHeaderWithoutInventingTrust(t *testing.T) {
	for _, name := range []string{"selected-archive", "wrong-reviewed-cutover-sha", "wrong-selected-epoch", "disagreeing-import-header", "no-header-cannot-borrow-new-singleton", "legacy-fixed-old-row", "selected-header-with-fixed-old-row", "bad-archive-cannot-fallback"} {
		t.Run(name, func(t *testing.T) {
			h, record, archive := epochCustodyArchiveFixture(t)
			original := append([]byte(nil), record.OriginalDocuments[5].Raw...)
			var cut NamespaceCutover
			if e := bson.Unmarshal(original, &cut); e != nil {
				t.Fatal(e)
			}
			selected := h
			trust := OriginalHistoryEpochTrust{Epoch: h.Epoch, ActivationSHA256: h.ActivationSHA256, Database: h.Database, ProducerSourceSHA256: hash([]byte("reviewed source selection")), CutoverSHA256: directHistoryDigest(cut), OriginalHeader: &selected}
			archiveKey := Receipts + "/" + historyEpochTransitionID(h)
			foreign := cut
			foreign.Epoch = "71111111-1111-4111-8111-111111111111"
			foreignRaw, e := bson.Marshal(foreign)
			if e != nil {
				t.Fatal(e)
			}
			reader := &epochCustodyTestReader{rows: map[string][]byte{archiveKey: archive, Sequences + "/" + NamespaceCutoverID: foreignRaw}}
			switch name {
			case "wrong-reviewed-cutover-sha":
				trust.CutoverSHA256 = hash([]byte("different reviewed cutover"))
			case "wrong-selected-epoch":
				selected.Epoch = foreign.Epoch
			case "disagreeing-import-header":
				other := h
				other.MigrationID = "81111111-1111-4111-8111-111111111111"
				trust.Import = &OriginalHistoryImportTrust{Header: other}
			case "no-header-cannot-borrow-new-singleton":
				trust.OriginalHeader = nil
			case "legacy-fixed-old-row":
				trust.OriginalHeader = nil
				reader.rows = map[string][]byte{Sequences + "/" + NamespaceCutoverID: original}
			case "selected-header-with-fixed-old-row":
				reader.rows = map[string][]byte{Sequences + "/" + NamespaceCutoverID: original}
			case "bad-archive-cannot-fallback":
				bad := append([]byte(nil), archive...)
				bad[0] ^= 1
				reader.rows[archiveKey] = bad
				reader.rows[Sequences+"/"+NamespaceCutoverID] = original
			}
			chain := &originalHistoryChain{ctx: context.Background(), reader: reader, trust: trust, refs: map[string]namespaceimport.SourceRef{}}
			actual, e := chain.cutover()
			success := name == "selected-archive" || name == "legacy-fixed-old-row" || name == "selected-header-with-fixed-old-row"
			if !success {
				if e == nil {
					t.Fatal("foreign/missing/corrupt source selection accepted", actual)
				}
				return
			}
			if e != nil || directHistoryDigest(actual) != trust.CutoverSHA256 || len(chain.refs) != 1 {
				t.Fatal("selected original custody lost", actual, e)
			}
			if name == "selected-archive" {
				if len(reader.calls) != 1 || reader.calls[0] != archiveKey {
					t.Fatal("archive caller read another epoch's singleton", reader.calls)
				}
				for _, ref := range chain.refs {
					if ref.RecordSHA256 != hash(archive) || ref.RecordSHA256 == hash(original) {
						t.Fatal("embedded raw acquired invented SourceRef")
					}
				}
			}
		})
	}
}
