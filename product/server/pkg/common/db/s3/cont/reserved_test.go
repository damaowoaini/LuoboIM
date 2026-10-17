package cont

import "testing"

func TestInternalStoragePathsCannotBeAbsoluteUploadTargets(t *testing.T) {
	for _, key := range []string{"openim/data/owner-v2/scope/hash/12/key", "openim/data/hash/legacy", "openim/temp/key", "openim/direct/other-owner/key", "./openim/temp/key", "public/../openim/temp/key", "/openim/temp/key", `openim\data\owner-v2\key`, "openim", "%6fpenim/data/owner-v2/key", "openim%2fdata/key", "public/%252e%252e/openim/temp/key", "legacy/../file", "legacy//file"} {
		if !IsReservedWriteKey(key) {
			t.Fatal("reserved storage path was allowed")
		}
	}
	for _, key := range []string{"existing-admin-config.json", "legacy-assets/logo.png", "openim-other/file"} {
		if IsReservedWriteKey(key) {
			t.Fatal("unrelated legacy absolute name was blocked")
		}
	}
}
