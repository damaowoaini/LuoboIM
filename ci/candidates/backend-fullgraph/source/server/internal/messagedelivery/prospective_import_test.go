package messagedelivery

import (
	"context"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

func TestProspectiveImportRejectsBarePageAndUnboundWriterBeforeIO(t *testing.T) {
	w := &NamespaceImportWriter{}
	if e := w.stageProspectiveConsumer(context.Background(), nil, namespaceimport.VerifiedPage{}); e == nil {
		t.Fatal("bare page authorized prospective writes")
	}
	var absent *Store
	if _, e := absent.ReadMembershipWindow(context.Background(), "sg_group", "owner"); e == nil {
		t.Fatal("nil reader admitted current roster")
	}
	if _, e := absent.ReadMembershipWindows(context.Background(), "sg_group", []string{"owner"}); e == nil {
		t.Fatal("nil page reader admitted current roster")
	}
}

func TestOwnerHistoryImportAndHistoricalCounterRejectBareCapabilities(t *testing.T) {
	for _, name := range []string{"preserved-segment", "historical-counter"} {
		t.Run(name, func(t *testing.T) {
			w := &NamespaceImportWriter{}
			var e error
			if name == "preserved-segment" {
				e = w.stageOwnerHistorySegments(context.Background(), nil, namespaceimport.VerifiedPage{})
			} else {
				e = w.initializeHistoricalCounter(context.Background(), nil, namespaceimport.VerifiedPage{}, namespaceimport.OriginRecord{}, 0)
			}
			if e == nil {
				t.Fatal("bare value authorized private migration write")
			}
		})
	}
}
