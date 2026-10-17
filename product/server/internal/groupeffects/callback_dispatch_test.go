package groupeffects

import (
	"testing"
	"time"
)

func TestCallbackAcknowledgementIsStrictAndRejectsSemanticError(t *testing.T) {
	for _, body := range []string{`{}`, `{"actionCode":0,"errCode":0,"nextCode":0}`, `{"actionCode":0,"extra":"legacy-compatible"}`} {
		if !callbackACK([]byte(body)) {
			t.Fatal("legal legacy ACK rejected")
		}
	}
	for _, body := range []string{`null`, `[]`, ``, `{"actionCode":1}`, `{"errCode":1002}`, `{"nextCode":1}`, `{"errCode":1002,"ErrCode":0}`, `{"errCode":0,"errCode":0}`, `{} {}`, `{"errCode":"0"}`} {
		if callbackACK([]byte(body)) {
			t.Fatal("unknown/ambiguous callback outcome accepted as success")
		}
	}
}
func TestEndpointBindingIncludesDestinationNamespaceAndLogicalAction(t *testing.T) {
	key := []byte("synthetic signer only")
	base, err := NewEndpoint("https://callback.fixture.invalid/base", "tenant-a", AfterQuit, key, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 'X'
	if string(base.secret) != "synthetic signer only" {
		t.Fatal("endpoint borrowed caller's signer")
	}
	for _, tc := range []struct {
		base, ns string
		name     CallbackName
	}{{"https://other.fixture.invalid/base", "tenant-a", AfterQuit}, {"https://callback.fixture.invalid/base", "tenant-b", AfterQuit}, {"https://callback.fixture.invalid/base", "tenant-a", AfterKick}} {
		x, err := NewEndpoint(tc.base, tc.ns, tc.name, []byte("synthetic"), time.Second)
		if err != nil || x.Binding() == base.Binding() {
			t.Fatal("different trusted callback route shared a binding")
		}
	}
	// Rotating the signer does not silently change the frozen business target.
	rotated, err := NewEndpoint("https://callback.fixture.invalid/base", "tenant-a", AfterQuit, []byte("rotated synthetic"), 2*time.Second)
	if err != nil || rotated.Binding() != base.Binding() {
		t.Fatal("signer rotation retargeted a frozen effect")
	}
}
func TestEndpointRejectsCredentialURLQueryAliasAndUnboundedTimeout(t *testing.T) {
	for _, raw := range []string{"https://u:p@callback.fixture.invalid", "https://callback.fixture.invalid/?token=not-used", "https://callback.fixture.invalid/#fragment", "https://callback.fixture.invalid/a/../b", "https://callback.fixture.invalid/a%2fb", "file:///tmp/callback"} {
		if _, err := NewEndpoint(raw, "tenant", AfterQuit, []byte("synthetic"), time.Second); err == nil {
			t.Fatal("unbound endpoint accepted")
		}
	}
	if _, err := NewEndpoint("https://callback.fixture.invalid", "tenant", AfterQuit, nil, time.Second); err == nil {
		t.Fatal("empty signer accepted")
	}
	if _, err := NewEndpoint("https://callback.fixture.invalid", "tenant", AfterQuit, []byte("synthetic"), time.Minute); err == nil {
		t.Fatal("unbounded callback accepted")
	}
}
