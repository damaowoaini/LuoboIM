package main

import (
	"bytes"
	"encoding/json"
	"io"
)

func decodeDoc(raw []byte, allowed map[string]bool, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errInput
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return errInput
		}
		seen[key] = true
		var discard json.RawMessage
		if d.Decode(&discard) != nil {
			return errInput
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return errInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return errInput
	}
	return nil
}
