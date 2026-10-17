package mgo

import (
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"go.mongodb.org/mongo-driver/bson"
)

func TestGroupAuthorityModelFilterKeepsDismissalMonotone(t *testing.T) {
	for _, fields := range []map[string]any{
		{"status": int32(constant.GroupOk)},
		{"status": int32(constant.GroupStatusMuted)},
		{"group_name": "new"},
		{"status": int32(constant.GroupStatusDismissed), "group_name": "new"},
	} {
		filter, err := groupAuthorityWriteFilter("g", fields)
		want := bson.M{"group_id": "g", "status": bson.M{"$ne": int32(constant.GroupStatusDismissed)}}
		if err != nil || !reflect.DeepEqual(filter, want) {
			t.Fatalf("terminal-state predicate missing: %#v %v", filter, err)
		}
	}
	filter, err := groupAuthorityWriteFilter("g", map[string]any{"status": int32(constant.GroupStatusDismissed)})
	if err != nil || !reflect.DeepEqual(filter, bson.M{"group_id": "g"}) {
		t.Fatal("explicit repeat-dismissal hard cleanup blocked")
	}
	if _, err := groupAuthorityWriteFilter("g", map[string]any{"status": "0"}); err == nil {
		t.Fatal("malformed status accepted")
	}
}
