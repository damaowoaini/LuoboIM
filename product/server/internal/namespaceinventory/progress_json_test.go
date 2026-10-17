package namespaceinventory

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

// The original generic decoder remains the executable reference, not a copied
// interpretation of its rules. A cold page must retain all nested alias/grammar
// rejection and typed field checks; no memo hit or new JSON permission is used.
func TestNamespaceProgressFastJSONPreservesOriginalStrictness(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"page", []byte(`{"before":{"phase":"mongo"},"after":{"phase":"done"},"mongo":[],"redis":[]}`)},
		{"same-key-different-objects", []byte(`{"x":1,"nested":{"x":2}}`)},
		{"duplicate-root", []byte(`{"x":1,"x":2}`)},
		{"case-fold-root", []byte(`{"X":1,"x":2}`)},
		{"escaped-key-alias", []byte(`{"\u0078":1,"X":2}`)},
		{"nested-object-alias", []byte(`{"rows":[{"x":{"N":1,"n":2}}]}`)},
		{"unicode-key-alias", []byte(`{"Ä":1,"ä":2}`)},
		{"surrogate-key-alias", []byte(`{"\ud800":1,"\udfff":2}`)},
		{"strings-are-not-objects", []byte(`{"s":"escaped \" key: } [ \\ ","t":null}`)},
		{"depth64", []byte(strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64))},
		{"depth65", []byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65))},
		{"extra-value", []byte(`{} {}`)},
		{"bad-comma", []byte(`{"x":1,}`)},
		{"empty", nil},
		{"unknown-field", []byte(`{"unknown":true}`)},
		{"invalid-typed-page", []byte(`{"mongo":[{"rawBSON":"!invalid-base64"}]}`)},
		{"number-max-finite", []byte(`1.7976931348623157e308`)},
		{"number-overflow-positive", []byte(`1e400`)},
		{"number-overflow-negative", []byte(`-1e400`)},
		{"number-underflow-positive", []byte(`1e-4000`)},
		{"number-underflow-negative", []byte(`-1e-4000`)},
		{"number-min-positive-subnormal", []byte(`5e-324`)},
		{"known-number-overflow", []byte(`{"before":{"redisCursor":1e400}}`)},
		{"known-uint64-overflow", []byte(`{"before":{"redisCursor":18446744073709551616}}`)},
		{"known-number-noninteger", []byte(`{"before":{"collection":0.5}}`)},
		{"known-number-wrong-shape", []byte(`{"before":{"collection":{"value":1}}}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := json.NewDecoder(bytes.NewReader(tc.raw))
			oldErr := uniqueJSON(original, 0)
			if oldErr == nil {
				if _, err := original.Token(); err != io.EOF {
					oldErr = ErrInvalid
				}
			}
			if (uniqueProgressJSON(tc.raw) == nil) != (oldErr == nil) {
				t.Fatal("fast key validation changed original strict JSON contract")
			}
			var old, now Page
			oldErr = DecodeStrict(tc.raw, &old)
			nowErr := decodeProgressPageStrict(tc.raw, &now)
			if (oldErr == nil) != (nowErr == nil) || oldErr == nil && !reflect.DeepEqual(old, now) {
				t.Fatal("cold page decoder changed original typed data or rejection")
			}
		})
	}
}
