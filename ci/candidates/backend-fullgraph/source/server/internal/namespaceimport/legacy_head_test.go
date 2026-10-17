package namespaceimport

import (
	"testing"
)

func TestLegacyMongoCounterAbsenceDoesNotClaimZero(t *testing.T) {
	// Synthetic shape controls do not attest a deployment or original absence.
	h := Header{Tenant: "tenant", Database: "db", StorageInstance: "storage", ReplicaSet: "rs", MongoProcess: "process", MongoTopology: 1, RedisRunID: "redis", KafkaClusterID: "cluster", Epoch: "12345678-1234-1234-1234-123456789001", ActivationSHA256: digest("activation"), MigrationID: "12345678-1234-1234-1234-123456789002", DeploymentPolicySHA256: digest("policy")}
	i := Inventory{HeaderSHA256: h.SHA256(), RawRootSHA256: digest("raw"), HeldAdmissionGuardSHA256: digest("guard")}
	ref := SourceRef{ObjectSHA256: digest("object"), RecordSHA256: digest("record"), Kind: "original-row", Position: 1}
	a := LegacyCounterAbsence{CID: "single_A_B", Collection: "message_delivery_sequences_v1", SnapshotSHA256: digest("snapshot"), RawRootSHA256: i.RawRootSHA256, RowsRootSHA256: digest("rows"), CollectionRootSHA256: digest("empty-collection"), AllocatorSourceSHA256: digest("reviewed-allocator"), AdmissionGuardSHA256: i.HeldAdmissionGuardSHA256}
	r := OriginRecord{Origin: NamespaceOriginDescriptor{CID: a.CID}, MongoHead: Head{State: LegacyCounterNotAllocatedHere, LegacyAbsence: &a}, RedisHead: Head{State: "present", Value: 71, Evidence: []SourceRef{ref}}}
	t.Run("only actual Redis contributes", func(t *testing.T) {
		got, e := r.EffectiveSeedMaximum()
		if e != nil || got != 71 || !originHeadsValid(h, i, r) {
			t.Fatalf("absence lost: %d %v", got, e)
		}
		if r.MongoHead.State == "present" || r.MongoHead.State == "proven-empty" {
			t.Fatal("absence became known zero")
		}
	})
	t.Run("neither plane known rejected", func(t *testing.T) {
		bad := clone(r)
		bad.RedisHead = Head{State: LegacyCounterNotAllocatedHere, LegacyAbsence: &a}
		if _, e := bad.EffectiveSeedMaximum(); e == nil {
			t.Fatal("missing both planes accepted")
		}
	})
	t.Run("nonzero absent sentinel rejected", func(t *testing.T) {
		bad := clone(r)
		bad.MongoHead.Value = 1
		if _, e := bad.EffectiveSeedMaximum(); e == nil {
			t.Fatal("absence contributed a number")
		}
	})
	t.Run("foreign raw root or held guard rejected", func(t *testing.T) {
		for _, which := range []string{"raw", "guard", "cid"} {
			bad := clone(r)
			switch which {
			case "raw":
				bad.MongoHead.LegacyAbsence.RawRootSHA256 = digest("foreign")
			case "guard":
				bad.MongoHead.LegacyAbsence.AdmissionGuardSHA256 = digest("foreign")
			case "cid":
				bad.MongoHead.LegacyAbsence.CID = "other"
			}
			if originHeadsValid(h, i, bad) {
				t.Fatal(which)
			}
		}
	})
	t.Run("known heads reject attached absence", func(t *testing.T) {
		bad := clone(r)
		bad.MongoHead.State = "present"
		if _, e := bad.EffectiveSeedMaximum(); e == nil {
			t.Fatal("mixed known and absent")
		}
	})
	t.Run("maximum excludes missing plane but preserves known higher head", func(t *testing.T) {
		bad := clone(r)
		bad.MongoHead = Head{State: "present", Value: 93, Evidence: []SourceRef{ref}}
		got, e := bad.EffectiveSeedMaximum()
		if e != nil || got != 93 {
			t.Fatal(got, e)
		}
	})
}
