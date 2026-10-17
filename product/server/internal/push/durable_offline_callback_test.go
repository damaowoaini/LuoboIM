package push

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func TestOfflineCallbackParserKeepsLegacyEmptyOverrideInfoAndRejection(t *testing.T) {
	for _, raw := range []string{`{}`, `{"userIDList":null}`, `{"userIDList":[]}`, `{"errCode":123,"actionCode":0,"vendorMetadata":{"known":"ignored"}}`} {
		state, users, info := parseOfflineCallback(http.StatusOK, []byte(raw), true)
		if state != "acknowledged" || len(users) > 0 || info != nil {
			t.Fatal("legacy default response no longer keeps original routing")
		}
	}
	state, users, info := parseOfflineCallback(http.StatusOK, []byte(`{"userIDList":["member","owner"],"offlinePushInfo":{"title":"synthetic","desc":"description","iOSBadgeCount":true,"signalInfo":"signal"}}`), true)
	if state != "acknowledged" || !reflect.DeepEqual(users, []string{"member", "owner"}) || info == nil || info.Title != "synthetic" || !info.IOSBadgeCount || info.SignalInfo != "signal" {
		t.Fatal("legacy legal output lost")
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		s, _, _ := parseOfflineCallback(status, []byte(`{}`), true)
		if s != "rejected" {
			t.Fatal("auth rejection became continue-compatible unknown")
		}
	}
	for _, raw := range []string{`{"actionCode":1,"errCode":1002}`, `{"nextCode":1}`} {
		s, _, _ := parseOfflineCallback(http.StatusOK, []byte(raw), true)
		if s != "rejected" {
			t.Fatal("semantic rejection accepted")
		}
	}
}
func TestOfflineCallbackParserAmbiguousNestedOutputIsUnknown(t *testing.T) {
	for _, raw := range []string{`{"actionCode":0,"ActionCode":1}`, `{"ActionCode":0}`, `{"userIDList":[],"userIDList":["foreign"]}`, `{"offlinePushInfo":{"title":"a","title":"b"}}`, `{"offlinePushInfo":{"Title":"a"}}`, `{"offlinePushInfo":{"desc":"a","DESC":"b"}}`, `{"userIDList":{},"actionCode":0}`, `null`, `{} {}`} {
		state, users, info := parseOfflineCallback(http.StatusOK, []byte(raw), true)
		if state != "unknown" || users != nil || info != nil {
			t.Fatal("ambiguous response acquired callback decision")
		}
	}
	if state, _, _ := parseOfflineCallback(http.StatusOK, []byte(`{}`), false); state != "unknown" {
		t.Fatal("partial body accepted")
	}
}
func TestOfflineCallbackBodyUsesOriginalCompleteLegacyDTO(t *testing.T) {
	m := &sdkws.MsgData{SendID: "owner", GroupID: "group", ClientMsgID: "synthetic-original", SenderPlatformID: 10, Content: []byte(`{"content":"body"}`), OfflinePushInfo: &sdkws.OfflinePushInfo{Title: "original", IOSBadgeCount: true}}
	input := messagedelivery.KafkaOfflineCallbackInput{OperationID: "synthetic-operation", Message: m, Users: []string{"first", "second", "third"}}
	raw, err := offlineCallbackBody(input)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("DTO JSON")
	}
	var users []string
	_ = json.Unmarshal(fields["userIDList"], &users)
	if !reflect.DeepEqual(users, input.Users) || string(fields["clientMsgID"]) != `"synthetic-original"` || string(fields["title"]) != `"original"` || string(fields["iOSBadgeCount"]) != "true" {
		t.Fatal("callback DTO was paged or original identity/info lost")
	}
	if _, found := fields["token"]; found {
		t.Fatal("callback disclosed ambient credentials")
	}
}
