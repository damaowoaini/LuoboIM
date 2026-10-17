package bufferplan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMediaPlansKeepProtocolUUIDsAndFillMissingSizesWithoutDroppingExtensions(t *testing.T) {
	for _, tc := range []struct {
		name, elem string
		kind       int
		lengths    [2]int64
		count      int
	}{
		{"picture", `"pictureElem":{"sourcePicture":{"uuid":"picture/a.png","size":0,"vendor":true}}`, 102, [2]int64{4, -1}, 1},
		{"sound", `"soundElem":{"uuid":"sound/a.ogg","dataSize":0}`, 103, [2]int64{4, -1}, 1},
		{"video", `"videoElem":{"videoUUID":"video/a.mp4","snapshotUUID":"snapshot/a.jpg","videoSize":0,"snapshotSize":0}`, 104, [2]int64{4, 3}, 2},
		{"file", `"fileElem":{"uuid":"file/ 文件名 .txt ","fileSize":0}`, 105, [2]int64{0, -1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal([]byte("{"+tc.elem+"}"), &fields); err != nil {
				t.Fatal(err)
			}
			fields["clientMsgID"], fields["contentType"], fields["extension"] = "client-a", tc.kind, map[string]any{"preserved": true}
			raw, _ := json.Marshal(fields)
			plan, err := Prepare(string(raw), "peer-a", "", tc.lengths)
			if err != nil || len(plan.Files) != tc.count {
				t.Fatalf("plan: %+v %v", plan, err)
			}
			if !strings.Contains(plan.JSON, `"preserved":true`) {
				t.Fatal("protocol extension lost")
			}
			for _, file := range plan.Files {
				if file.Size != tc.lengths[file.Slot] || !UUID(file.UUID) {
					t.Fatal(file)
				}
			}
			if tc.name == "picture" && (!strings.Contains(plan.JSON, `"size":4`) || !strings.Contains(plan.JSON, `"vendor":true`)) {
				t.Fatal(plan.JSON)
			}
			if tc.name == "video" && (!strings.Contains(plan.JSON, `"videoSize":4`) || !strings.Contains(plan.JSON, `"snapshotSize":3`)) {
				t.Fatal(plan.JSON)
			}
			if tc.name == "file" && plan.Files[0].UUID != "file/ 文件名 .txt " {
				t.Fatal("opaque UUID was normalized")
			}
		})
	}
}

func TestVideoAndMediaMetadataRejectBeforeRegistrationOnEitherBufferFailure(t *testing.T) {
	base := `{"clientMsgID":"client-a","contentType":104,"videoElem":{"videoUUID":"video/a.mp4","snapshotUUID":"snapshot/a.jpg","videoSize":4,"snapshotSize":3}}`
	for _, tc := range []struct {
		raw     string
		lengths [2]int64
	}{
		{base, [2]int64{4, -1}}, {base, [2]int64{4, 0}}, {base, [2]int64{4, 2}},
		{strings.Replace(base, `"videoSize":4`, `"videoSize":5`, 1), [2]int64{4, 3}},
		{strings.Replace(base, `snapshot/a.jpg`, `video/a.mp4`, 1), [2]int64{4, 3}},
		{strings.Replace(base, `"snapshotUUID":"snapshot/a.jpg",`, ``, 1), [2]int64{4, 3}},
		{strings.Replace(base, `"snapshotSize":3`, `"snapshotSize":-1`, 1), [2]int64{4, 3}},
	} {
		if _, err := Prepare(tc.raw, "peer-a", "", tc.lengths); err == nil {
			t.Fatal("invalid pair accepted", tc)
		}
	}
	if _, err := Prepare(base, "peer-a", "group-a", [2]int64{4, 3}); err == nil {
		t.Fatal("ambiguous receiver")
	}
	if _, err := Prepare(`[]`, "peer-a", "", [2]int64{4, 3}); err == nil {
		t.Fatal("nonobject message")
	}
}

func TestReadyMediaAndTextPreserveStandardSendWithoutFileRegistration(t *testing.T) {
	for _, raw := range []string{
		`{"clientMsgID":"text-a","contentType":101,"textElem":{"content":"hello"}}`,
		`{"clientMsgID":"sent-a","contentType":102,"status":2,"pictureElem":{"sourcePicture":{"url":"https://media.invalid/p.png"}}}`,
	} {
		lengths := [2]int64{-1, -1}
		if strings.Contains(raw, `"status":2`) {
			lengths = [2]int64{4, 3}
		}
		plan, err := Prepare(raw, "peer-a", "", lengths)
		if err != nil || len(plan.Files) != 0 || plan.JSON != raw {
			t.Fatal(plan, err)
		}
	}
	if _, err := Prepare(`{"clientMsgID":"sent-a","contentType":102,"status":2,"pictureElem":{"sourcePicture":{}}}`, "peer-a", "", [2]int64{-1, -1}); err == nil {
		t.Fatal("missing uploaded media URL")
	}
}
