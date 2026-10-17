package user

import (
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
)

func TestBootstrapUsersDeduplicatesManagerAndNotificationIDs(t *testing.T) {
	users, err := bootstrapUsers([]string{"shared", "manager", "shared"}, []string{"manager name", "manager", "duplicate"}, []string{"shared", "notification", "notification"}, []string{"notification name", "notice", "duplicate"})
	if err != nil || len(users) != 3 {
		t.Fatal("overlapping bootstrap configuration must produce three unique accounts", err)
	}
	if users[0].UserID != "shared" || users[0].AppMangerLevel != constant.AppAdmin || users[0].Nickname != "manager name" || users[2].AppMangerLevel != constant.AppNotificationAdmin {
		t.Fatal("overlap lost the management role or deterministic initial profile")
	}
	if _, err := bootstrapUsers([]string{"id"}, nil, nil, nil); err == nil {
		t.Fatal("incomplete bootstrap configuration accepted")
	}
	if _, err := bootstrapUsers(nil, nil, []string{" "}, []string{"name"}); err == nil {
		t.Fatal("empty bootstrap ID accepted")
	}
}
