package third

import (
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/objectverify"
)

func TestThirdCompletionStableURLUsesCanonicalSinglePathEscape(t *testing.T) {
	s := &thirdServer{apiURL: "https://media.fixture.invalid/api/object/"}
	for _, name := range []string{"owner/normal.png", "owner/中文 证据.png", "owner/a#b?.png", "owner/50%.png", "owner/literal%2Fname.png"} {
		got := s.apiAddress(name)
		if got != objectverify.CanonicalObjectURL(s.apiURL, name) {
			t.Fatal("completed URL differs from private evidence verifier's canonical issuing contract")
		}
		if name == "owner/literal%2Fname.png" && got != s.apiURL+"owner/literal%252Fname.png" {
			t.Fatal("literal percent escape was decoded as a slash")
		}
	}
}
