package messagedelivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestNotificationFactsPagesBoundDocumentsWithoutRosterCap(t *testing.T) {
	// Above the old generic50k cap; this is facts serialization, not a fake group.
	ids := make([]string, 50001)
	for i := range ids {
		ids[i] = fmt.Sprintf("synthetic-%08d", i)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	blob := factBlob("branch-recipients-json", raw)
	if blob.Pages < 2 || blob.Bytes != int64(len(raw)) {
		t.Fatal("roster was capped or not paged")
	}
	var rebuilt bytes.Buffer
	pages := int64(0)
	for start := 0; start < len(raw); {
		end := start + min(ownedNotificationFactsPageBytes, len(raw)-start)
		chunk := raw[start:end]
		if len(chunk) > 32*1024 {
			t.Fatal("unbounded page")
		}
		rebuilt.Write(chunk)
		pages++
		start = end
	}
	if pages != blob.Pages || !bytes.Equal(rebuilt.Bytes(), raw) || hash(rebuilt.Bytes()) != blob.SHA256 {
		t.Fatal("paged facts truncated or changed original roster")
	}
}
