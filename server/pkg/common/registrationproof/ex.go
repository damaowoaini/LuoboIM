// Package registrationproof protects server-owned account-creation markers
// without changing OpenIM's external UserInfo.Ex string protocol.
package registrationproof

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
)

const PublicKey = "luoboRegistrationID"
const AdminKey = "luoboAdminProvisioningID"

var ErrProtected = errors.New("用户扩展资料含无效或受保护的开户字段")

type object map[string]any

func reserved(key string) bool {
	return strings.EqualFold(key, PublicKey) || strings.EqualFold(key, AdminKey)
}

// strictValue rejects duplicate keys at every structured depth. Reserved keys
// are canonical and top-level: encoding/json's struct decoding otherwise also
// accepts case/Unicode-folded aliases for the same protected proof field.
func strictValue(d *json.Decoder, depth int) (any, error) {
	if depth > 128 {
		return nil, ErrProtected
	}
	token, e := d.Token()
	if e != nil {
		return nil, e
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delimiter {
	case '{':
		value := object{}
		for d.More() {
			raw, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := raw.(string)
			if !ok {
				return nil, ErrProtected
			}
			if _, found := value[key]; found {
				return nil, ErrProtected
			}
			if depth == 0 && reserved(key) && key != PublicKey && key != AdminKey {
				return nil, ErrProtected
			}
			child, e := strictValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			value[key] = child
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, ErrProtected
		}
		return value, nil
	case '[':
		value := []any{}
		for d.More() {
			child, e := strictValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			value = append(value, child)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, ErrProtected
		}
		return value, nil
	}
	return nil, ErrProtected
}
func parse(ex string) (object, bool, error) {
	text := strings.TrimSpace(ex)
	if text == "" || (text[0] != '{' && text[0] != '[') {
		return nil, false, nil
	} // Legacy opaque Ex remains supported.
	d := json.NewDecoder(strings.NewReader(ex))
	d.UseNumber()
	value, e := strictValue(d, 0)
	if e != nil {
		return nil, false, ErrProtected
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, false, ErrProtected
	}
	result, ok := value.(object)
	return result, ok, nil
}
func markers(o object) object {
	result := object{}
	for _, key := range []string{PublicKey, AdminKey} {
		if v, exists := o[key]; exists {
			result[key] = v
		}
	}
	return result
}
func same(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// ProtectUpdate preserves only server-owned fields from the authoritative old
// value; ordinary Ex fields keep their original whole-value replacement
// semantics. Omitted proof keys are restored. Empty writes clear custom fields
// while retaining proof. Non-JSON writes cannot erase an existing proof.
func ProtectUpdate(oldEx, newEx string) (string, error) {
	if oldEx == newEx {
		return oldEx, nil
	}
	old, _, e := parse(oldEx)
	if e != nil {
		return "", ErrProtected
	}
	next, structured, e := parse(newEx)
	if e != nil {
		return "", ErrProtected
	}
	previous := markers(old)
	if !structured {
		if len(previous) == 0 {
			return newEx, nil
		}
		if strings.TrimSpace(newEx) != "" {
			return "", ErrProtected
		}
		next = object{}
	}
	for _, key := range []string{PublicKey, AdminKey} {
		oldValue, existed := previous[key]
		newValue, supplied := next[key]
		if supplied && (!existed || !same(oldValue, newValue)) {
			return "", ErrProtected
		}
		if existed {
			next[key] = oldValue
		}
	}
	if len(previous) == 0 {
		return newEx, nil
	}
	encoded, e := json.Marshal(next)
	if e != nil {
		return "", ErrProtected
	}
	return string(encoded), nil
}

// InitialMarkers rejects forged aliases, malformed values and cross-namespace
// ambiguity even for otherwise trusted registration callers. Marker-free raw
// strings remain compatible with existing users and bootstrap integrations.
func InitialMarkers(ex string) (map[string]string, error) {
	value, _, e := parse(ex)
	if e != nil {
		return nil, e
	}
	result := map[string]string{}
	for key, raw := range markers(value) {
		id, ok := raw.(string)
		if !ok {
			return nil, ErrProtected
		}
		parsed, e := uuid.Parse(id)
		if e != nil || parsed.Version() != 4 || parsed.String() != id {
			return nil, ErrProtected
		}
		result[key] = id
	}
	if len(result) > 1 {
		return nil, ErrProtected
	}
	return result, nil
}

type initialKey struct{}
type grant map[string]map[string]string

func withInitial(ctx context.Context, allowed grant) context.Context {
	return context.WithValue(ctx, initialKey{}, allowed)
}

// CheckCreate is also called by the Mongo writer, so full-model/batch/bootstrap
// insertions cannot mint protected values by avoiding an RPC field filter.
func CheckCreate(ctx context.Context, userID, ex string) error {
	m, e := InitialMarkers(ex)
	if e != nil {
		return e
	}
	if len(m) == 0 {
		return nil
	}
	allowed, _ := ctx.Value(initialKey{}).(grant)
	if allowed == nil || !same(allowed[userID], m) {
		return ErrProtected
	}
	return nil
}

// CheckUpdateMap closes dotted-path/operator/full-map bypasses before Mongo
// interprets a caller's keys. Every current legitimate profile field is flat.
func CheckUpdateMap(args map[string]any) error {
	for key := range args {
		if key == "" || strings.HasPrefix(key, "$") || strings.ContainsAny(key, ".\x00") || strings.EqualFold(key, "user_id") || strings.EqualFold(key, "_id") || (strings.EqualFold(key, "ex") && key != "ex") {
			return ErrProtected
		}
	}
	if ex, exists := args["ex"]; exists {
		if _, ok := ex.(string); !ok {
			return ErrProtected
		}
	}
	return nil
}
