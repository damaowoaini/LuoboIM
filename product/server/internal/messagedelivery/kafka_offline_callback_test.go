package messagedelivery

import (
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
)

func TestKafkaOfflineCallbackDecisionPreservesLegacyNilEmptyAndInfoSemantics(t *testing.T) {
	row := offlineCallbackRow{Allowed: offlineHashes([]string{"a", "b"}), Defaults: offlineHashes([]string{"a"})}
	for _, users := range [][]string{nil, {}} {
		selected, replace, info, err := offlineDecision(row, users, nil)
		if err != nil || !reflect.DeepEqual(selected, row.Defaults) || replace || len(info) > 0 {
			t.Fatal("legacy nil/empty response replaced original recipients", err)
		}
	}
	selected, replace, info, err := offlineDecision(row, []string{"b"}, &sdkws.OfflinePushInfo{Title: "synthetic title", Desc: "synthetic description", IOSBadgeCount: true})
	if err != nil || !reflect.DeepEqual(selected, offlineHashes([]string{"b"})) || !replace || len(info) == 0 {
		t.Fatal("legal original read-cap expansion or info replacement lost", err)
	}
	if _, _, _, err := offlineDecision(row, []string{"foreign"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign UID acquired read permission", err)
	}
	if _, _, _, err := offlineDecision(row, []string{"a", "a"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate recipient ambiguity accepted", err)
	}
}
func TestKafkaOfflineCallbackOutputDigestBindsSelectionReplacementAndInfo(t *testing.T) {
	original := offlineOutputSHA(offlineHashes([]string{"a"}), false, nil)
	for _, candidate := range []string{offlineOutputSHA(offlineHashes([]string{"b"}), false, nil), offlineOutputSHA(offlineHashes([]string{"a"}), true, nil), offlineOutputSHA(offlineHashes([]string{"a"}), true, []byte{1})} {
		if candidate == original {
			t.Fatal("callback output digest lost a semantic field")
		}
	}
}
