package adminprovision

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
)

const publicIMMarker = "luoboRegistrationID"
const adminIMMarker = "luoboAdminProvisioningID"

// ReadIMMarker does not treat legacy/ambiguous JSON aliases as authoritative
// ownership. In particular encoding/json struct decoding normally accepts
// duplicate keys, case aliases and Unicode folding for the protected fields.
func ReadIMMarker(ex string) (id, purpose string, err error) {
	purpose = AdminPurpose
	text := strings.TrimSpace(ex)
	if text == "" || text[0] != '{' {
		return "", purpose, nil
	}
	d := json.NewDecoder(strings.NewReader(ex))
	d.UseNumber()
	value, e := readMarkerJSON(d, 0)
	if e != nil {
		return "", "", e
	}
	if _, e := d.Token(); e != io.EOF {
		return "", "", errors.New("ambiguous IM ownership JSON")
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return "", "", errors.New("invalid IM ownership object")
	}
	for _, key := range []string{adminIMMarker, publicIMMarker} {
		v, exists := fields[key]
		if !exists {
			continue
		}
		if id != "" {
			return "", "", errors.New("ambiguous IM ownership namespaces")
		}
		var valid bool
		id, valid = v.(string)
		parsed, e := uuid.Parse(id)
		if !valid || e != nil || parsed.Version() != 4 || parsed.String() != id {
			return "", "", errors.New("invalid IM ownership marker")
		}
		if key == publicIMMarker {
			purpose = PublicPurpose
		}
	}
	return id, purpose, nil
}
func readMarkerJSON(d *json.Decoder, depth int) (any, error) {
	if depth > 128 {
		return nil, errors.New("IM ownership JSON too deep")
	}
	token, e := d.Token()
	if e != nil {
		return nil, e
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delim {
	case '{':
		fields := map[string]any{}
		for d.More() {
			t, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := t.(string)
			if !ok {
				return nil, errors.New("invalid IM ownership JSON key")
			}
			if _, exists := fields[key]; exists {
				return nil, errors.New("duplicate IM ownership JSON key")
			}
			if depth == 0 && ((strings.EqualFold(key, publicIMMarker) && key != publicIMMarker) || (strings.EqualFold(key, adminIMMarker) && key != adminIMMarker)) {
				return nil, errors.New("noncanonical IM ownership key")
			}
			value, e := readMarkerJSON(d, depth+1)
			if e != nil {
				return nil, e
			}
			fields[key] = value
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, errors.New("invalid IM ownership object")
		}
		return fields, nil
	case '[':
		values := []any{}
		for d.More() {
			v, e := readMarkerJSON(d, depth+1)
			if e != nil {
				return nil, e
			}
			values = append(values, v)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, errors.New("invalid IM ownership array")
		}
		return values, nil
	}
	return nil, errors.New("invalid IM ownership JSON")
}
