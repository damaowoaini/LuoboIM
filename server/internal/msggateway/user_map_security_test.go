package msggateway

import (
	"sync"
	"testing"
)

func TestUserMapSnapshotCannotMutateLiveSession(t *testing.T) {
	m := newUserMap()
	original := &Client{UserID: "owner", PlatformID: 2}
	m.Set("owner", original)
	snapshot, _ := m.GetAll("owner")
	snapshot[0] = &Client{UserID: "foreign"}
	actual, _ := m.GetAll("owner")
	if len(actual) != 1 || actual[0] != original {
		t.Fatal("RPC snapshot aliases live session slice")
	}
}
func TestUserMapRemoveExactClientRetainsReusedRemote(t *testing.T) {
	m := newUserMap()
	old := &Client{UserID: "owner", PlatformID: 2, ctx: &UserConnContext{RemoteAddr: "127.0.0.1:12345"}}
	fresh := &Client{UserID: "owner", PlatformID: 2, ctx: &UserConnContext{RemoteAddr: "127.0.0.1:12345"}}
	m.Set("owner", old)
	m.Set("owner", fresh)
	m.deleteClients("owner", []*Client{old})
	actual, _ := m.GetAll("owner")
	if len(actual) != 1 || actual[0] != fresh {
		t.Fatal("old pointer removal erased newer session at same remote address")
	}
}
func TestUserMapConcurrentSessionUpdatesDoNotLoseConnections(t *testing.T) {
	m := newUserMap()
	var wg sync.WaitGroup
	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Set("owner", &Client{UserID: "owner", PlatformID: 2}) }()
	}
	wg.Wait()
	actual, _ := m.GetAll("owner")
	if len(actual) != 128 {
		t.Fatalf("lost live sessions:%d/128", len(actual))
	}
}
