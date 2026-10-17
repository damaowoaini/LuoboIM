//go:build linux

package ownedcontrol

import (
	"github.com/IBM/sarama"
	"testing"
	"time"
)

func TestFrozenKafkaFrameBindsWholeOriginalBatchAndRangeProof(t *testing.T) {
	r := FrozenPartitionRange{ClusterID: "owned-cluster", Topic: "MsgToModify", Group: "old-modifier", Partition: 1, Start: 7, End: 8, Deadline: time.Now().UTC().Add(20 * time.Second), ProofSHA256: sha([]byte("actual-proof"))}
	m := &sarama.ConsumerMessage{Topic: r.Topic, Partition: 1, Offset: 7, Key: []byte("original-key"), Value: []byte("synthetic-whole-batch"), Headers: []*sarama.RecordHeader{{Key: []byte("operationID"), Value: []byte("original")}}, Timestamp: time.Unix(100, 0).UTC()}
	first, e := frozenRecord(r, m)
	if e != nil {
		t.Fatal(e)
	}
	again, e := frozenRecord(r, m)
	if e != nil || first.RecordSHA256 != again.RecordSHA256 || first.SourceID != again.SourceID {
		t.Fatal("same original coordinate changed")
	}
	m.Value[0] ^= 1
	if first.Value[0] == m.Value[0] {
		t.Fatal("caller aliases original batch bytes")
	}
	changed, e := frozenRecord(r, m)
	if e != nil || changed.SourceID != first.SourceID || changed.RecordSHA256 == first.RecordSHA256 || changed.RawSHA256 == first.RawSHA256 {
		t.Fatal("coordinate reuse did not bind original raw")
	}
	r.ProofSHA256 = sha([]byte("foreign-proof"))
	foreign, e := frozenRecord(r, m)
	if e != nil || foreign.RecordSHA256 == changed.RecordSHA256 {
		t.Fatal("range proof omitted from record identity")
	}
	m.Offset = 8
	if _, e = frozenRecord(r, m); e == nil {
		t.Fatal("outside half-open range accepted")
	}
}
