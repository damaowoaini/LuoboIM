package namespaceinventory

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// The first progress memo fixed repeated row classification, but summary order
// is the index's SHA order, not physical page order. A bounded invocation-local
// memo therefore still has cold decodes. Keep the full strict JSON contract on
// each cold decode without Decoder.Token allocating an interface for every
// scalar/base64 payload. This scanner never authorizes/caches private bytes:
// page() still checks current RawRoot, private metadata and freshly reread SHA.
// json.Valid performs the complete JSON grammar check first. The walk below
// checks the SAME <=64 depth and strings.ToLower duplicate key contract as
// uniqueJSON, including escaped/non-ASCII keys and default float64 range.
// The ordinary typed decoder
// still rejects unknown fields and malformed BSON/base64/typed values.
func decodeProgressPageStrict(raw []byte, out *Page) error {
	if err := uniqueProgressJSON(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrInvalid
	}
	return nil
}

type progressJSONWalk struct {
	raw []byte
	at  int
}

func uniqueProgressJSON(raw []byte) error {
	if !json.Valid(raw) {
		return ErrInvalid
	}
	w := progressJSONWalk{raw: raw}
	if err := w.value(0); err != nil {
		return err
	}
	w.space()
	if w.at != len(raw) {
		return ErrInvalid
	}
	return nil
}
func (w *progressJSONWalk) space() {
	for w.at < len(w.raw) {
		switch w.raw[w.at] {
		case ' ', '\t', '\n', '\r':
			w.at++
		default:
			return
		}
	}
}

// Called only after json.Valid, so escaped quotes, unicode escapes and closing
// quotes have already passed the standard grammar. Values are never unescaped.
func (w *progressJSONWalk) quoted() (int, int, bool) {
	start := w.at
	w.at++
	plain := true
	for w.raw[w.at] != '"' {
		if w.raw[w.at] == '\\' {
			plain = false
			w.at += 2
			continue
		}
		if w.raw[w.at] >= 0x80 {
			plain = false
		}
		w.at++
	}
	w.at++
	return start, w.at, plain
}
func (w *progressJSONWalk) value(depth int) error {
	if depth > 64 {
		return ErrInvalid
	}
	w.space()
	switch w.raw[w.at] {
	case '{':
		w.at++
		w.space()
		if w.raw[w.at] == '}' {
			w.at++
			return nil
		}
		seen := map[string]bool{}
		for {
			start, end, plain := w.quoted()
			var key string
			if plain {
				key = string(w.raw[start+1 : end-1])
			} else if json.Unmarshal(w.raw[start:end], &key) != nil {
				return ErrInvalid
			}
			key = strings.ToLower(key)
			if seen[key] {
				return ErrInvalid
			}
			seen[key] = true
			w.space()
			w.at++ // validated colon
			if err := w.value(depth + 1); err != nil {
				return err
			}
			w.space()
			if w.raw[w.at] == '}' {
				w.at++
				return nil
			}
			w.at++
			w.space() // validated comma and next key
		}
	case '[':
		w.at++
		w.space()
		if w.raw[w.at] == ']' {
			w.at++
			return nil
		}
		for {
			if err := w.value(depth + 1); err != nil {
				return err
			}
			w.space()
			if w.raw[w.at] == ']' {
				w.at++
				return nil
			}
			w.at++ // validated comma
		}
	case '"':
		w.quoted()
		return nil
	default:
		start := w.at
	scan:
		for w.at < len(w.raw) {
			switch w.raw[w.at] {
			case ' ', '\t', '\r', '\n', ',', ']', '}':
				break scan
			default:
				w.at++
			}
		}
		// encoding/json Decoder.Token uses decodeState.convertNumber with
		// useNumber=false, hence strconv.ParseFloat(...,64). json.Valid alone does
		// not reject an overflowing but grammatically valid number such as 1e400.
		// Use the exact same conversion/error rule, including underflow rounding;
		// keywords are already grammar-validated and are not numeric values.
		first := w.raw[start]
		if first == '-' || first >= '0' && first <= '9' {
			if _, err := strconv.ParseFloat(string(w.raw[start:w.at]), 64); err != nil {
				return ErrInvalid
			}
		}
		return nil
	}
}
