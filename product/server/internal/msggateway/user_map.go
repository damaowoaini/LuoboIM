package msggateway

import "sync"

// The per-user slice is never published by reference. Register and RPC kick
// operations may run concurrently; each read/modify/write is atomic here.
type UserMap struct {
	mu      sync.RWMutex
	entries map[string][]*Client
}

func newUserMap() *UserMap { return &UserMap{entries: make(map[string][]*Client)} }
func (u *UserMap) GetAll(key string) ([]*Client, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	v, ok := u.entries[key]
	return append([]*Client(nil), v...), ok
}
func (u *UserMap) Get(key string, platform int) ([]*Client, bool, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	v, ok := u.entries[key]
	var found []*Client
	for _, c := range v {
		if c.PlatformID == platform {
			found = append(found, c)
		}
	}
	return found, ok, len(found) > 0
}
func (u *UserMap) Set(key string, client *Client) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.entries == nil {
		u.entries = make(map[string][]*Client)
	}
	u.entries[key] = append(u.entries[key], client)
}
func (u *UserMap) deleteClients(key string, clients []*Client) bool {
	remove := make(map[*Client]bool, len(clients))
	for _, c := range clients {
		remove[c] = true
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	old, ok := u.entries[key]
	if !ok {
		return false
	}
	keep := make([]*Client, 0, len(old))
	for _, c := range old {
		if !remove[c] {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		delete(u.entries, key)
		return true
	}
	u.entries[key] = keep
	return false
}
func (u *UserMap) DeleteAll(key string) { u.mu.Lock(); defer u.mu.Unlock(); delete(u.entries, key) }
