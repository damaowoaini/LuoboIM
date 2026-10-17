package namespaceinventory

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNamespaceProgressIndexDecodePreservesOriginalStrictOracle(t *testing.T) {
	// The two oracles are the unchanged original decoder and the standard typed
	// decoder. RawMessage deliberately lets typed JSON accept inner values that
	// the original all-depth uniqueness/float64 contract must still reject.
	value := func(raw string) string {
		return `{"kind":"raw-bound-index-v1","depth":0,"items":[{"key":"` + strings.Repeat("a", 64) + `","value":` + raw + `}]}`
	}
	valid := value(`{"source":"synthetic-only","number":7}`)
	cases := []struct {
		name, raw           string
		reject, typedReject bool
	}{
		{"valid-raw-value", valid, false, false},
		{"unknown-top-field", strings.TrimSuffix(valid, "}") + `,"unknown":1}`, true, true},
		{"escaped-case-alias", strings.TrimSuffix(valid, "}") + `,"\u004bIND":"replacement"}`, true, false},
		{"raw-nested-duplicate", value(`{"nested":{"key":1,"key":2}}`), true, false},
		{"raw-unicode-fold-alias", value(`{"Ä":1,"ä":2}`), true, false},
		{"raw-depth-accepted", value(strings.Repeat("[", 60) + "0" + strings.Repeat("]", 60)), false, false},
		{"raw-depth-overflow", value(strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)), true, false},
		{"raw-float64-overflow", value(`1e400`), true, false},
		{"raw-float64-underflow", value(`1e-400`), false, false},
		{"raw-largest-finite-float64", value(`1.7976931348623157e308`), false, false},
		{"typed-depth-range", strings.Replace(valid, `"depth":0`, `"depth":9223372036854775808`, 1), true, true},
		{"typed-depth-fraction", strings.Replace(valid, `"depth":0`, `"depth":1.5`, 1), true, true},
		{"typed-depth-shape", strings.Replace(valid, `"depth":0`, `"depth":"0"`, 1), true, true},
		{"typed-items-shape", `{"kind":"raw-bound-index-v1","depth":0,"items":{}}`, true, true},
		{"trailing-second-value", valid + ` {}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var original, standard, optimized progressNode
			oldErr := DecodeStrict([]byte(tc.raw), &original)
			typed := json.NewDecoder(bytes.NewReader([]byte(tc.raw)))
			typed.DisallowUnknownFields()
			typedErr := typed.Decode(&standard)
			gotErr := decodeProgressIndexNodeStrict([]byte(tc.raw), &optimized)
			if (oldErr != nil) != tc.reject || (typedErr != nil) != tc.typedReject {
				t.Fatal("original/standard decoder oracle precondition changed", oldErr, typedErr)
			}
			if (gotErr != nil) != (oldErr != nil) {
				t.Fatal("optimized index decode changed the original strict rejection", gotErr, oldErr)
			}
			if oldErr == nil && (!reflect.DeepEqual(original, optimized) || !reflect.DeepEqual(standard, optimized)) {
				t.Fatal("typed index values/RawMessage evidence changed")
			}
		})
	}
}
