package cont

import (
	"path"
	"strings"
)

// IsReservedWriteKey protects permanent proof objects and temporary capabilities
// even from the management-only legacy absolute form-upload option. Alias names
// are independent of provider keys and retain their existing API contract.
func IsReservedWriteKey(key string) bool {
	// Absolute provider targets must already be canonical. Refuse encoding,
	// backslash and traversal aliases instead of relying on provider-specific
	// URL decoding or path-cleaning behavior. Normal alias names are unaffected.
	if key == "" || strings.ContainsAny(key, "%\\\x00\r\n") {
		return true
	}
	canonical := path.Clean("/" + key)
	if canonical != "/"+key {
		return true
	}
	return canonical == "/openim" || strings.HasPrefix(canonical, "/openim/")
}
