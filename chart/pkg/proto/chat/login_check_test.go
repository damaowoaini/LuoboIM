package chat

import "testing"

func TestAccountOnlyLoginValidation(t *testing.T) {
	if err := (&LoginReq{Account: "alice", Password: "0123456789abcdef0123456789abcdef", Platform: 5}).Check(); err != nil {
		t.Fatal("account login rejected", err)
	}
	if err := (&LoginReq{Platform: 5}).Check(); err == nil {
		t.Fatal("missing account accepted")
	}
	if err := (&LoginReq{Account: "alice", Platform: 0}).Check(); err == nil {
		t.Fatal("invalid platform accepted")
	}
}
