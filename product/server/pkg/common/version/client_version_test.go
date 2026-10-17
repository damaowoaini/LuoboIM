package version

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientVersionReadsOnlyValidCommitResponse(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"valid", `{"sha":"` + sha + `"}`, 200, true}, {"missing hash", `{}`, 200, false},
		{"invalid hash", `{"sha":"not-a-commit"}`, 200, false}, {"API failure", `{"sha":"` + sha + `"}`, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer server.Close()
			got, err := fetchClientVersion(server.Client(), server.URL)
			if (err == nil) != tc.valid || (tc.valid && got != sha) {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}
