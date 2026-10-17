package namespaceinventory

import (
	"context"
	"testing"
)

func TestImportCaptureStampDoesNotRenewOnResumeOrSourceDrift(t *testing.T) {
	source, _, snapshot := importSnapshotFixture(t)
	ctx := context.Background()
	before := snapshot.Identity()
	calls := source.pageCalls
	first, e := snapshot.CaptureStamp(ctx, source)
	if e != nil || first.CaptureID != snapshot.session.ID || !first.CapturedAt.Equal(snapshot.session.Created) || first.SessionSHA256 != before.SessionSHA256 {
		t.Fatal("original capture metadata replaced", e)
	}
	again, e := OpenImportSnapshot(ctx, source, snapshot.ImportSnapshotPath(), source.id.Binding)
	if e != nil {
		t.Fatal(e)
	}
	next, e := again.CaptureStamp(ctx, source)
	if e != nil || next != first || jsonSHA(again.Identity()) != jsonSHA(before) || source.pageCalls != calls {
		t.Fatal("resume refreshed capture or recaptured source", e)
	}
	source.id.MongoTopology++
	if _, e = snapshot.CaptureStamp(ctx, source); e == nil {
		t.Fatal("restart accepted original stamp")
	}
}
