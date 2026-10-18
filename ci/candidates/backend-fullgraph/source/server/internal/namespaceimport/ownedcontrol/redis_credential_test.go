package ownedcontrol

import (
	"encoding/json"
	"errors"
	"testing"
)

func redisACLProtocolRepliesForTest() ([]interface{}, map[interface{}]interface{}) {
	return []interface{}{
		"flags", []interface{}{"on"}, "passwords", []interface{}{"private-hash"},
		"commands", "-@all +get +ping", "keys", "~USER_INFO:fixture", "channels", "&sensitive_word_reload",
		"selectors", []interface{}{[]interface{}{"commands", "-@all +get", "keys", "~USER_INFO:other", "channels", "&notice"}},
	}, map[interface{}]interface{}{
		"channels": "&sensitive_word_reload", "keys": "~USER_INFO:fixture", "commands": "-@all +get +ping",
		"passwords": []interface{}{"private-hash"}, "flags": []interface{}{"on"},
		"selectors": []interface{}{map[interface{}]interface{}{"channels": "&notice", "keys": "~USER_INFO:other", "commands": "-@all +get"}},
	}
}

func redisACLDisabledReplyForTest() map[interface{}]interface{} {
	return map[interface{}]interface{}{
		"flags": []interface{}{"off"}, "passwords": []interface{}{}, "commands": "-@all",
		"keys": "", "channels": "", "selectors": []interface{}{},
	}
}

func TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof(t *testing.T) {
	t.Run("equivalent-protocols-complete-summary-and-real-disabled-state", func(t *testing.T) {
		resp2, resp3 := redisACLProtocolRepliesForTest()
		// Independent complete wire-contract oracle: selectors must not disappear
		// and raw interface-key maps must not reach encoding/json.
		want := `{"channels":"\u0026sensitive_word_reload","commands":"-@all +get +ping","flags":["on"],"keys":"~USER_INFO:fixture","passwords":["private-hash"],"selectors":[{"channels":"\u0026notice","commands":"-@all +get","keys":"~USER_INFO:other"}]}`
		var digest string
		for _, reply := range []any{resp2, resp3} {
			row, err := redisUser(reply)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(row)
			if err != nil || string(body) != want || redisDisabled(row) {
				t.Fatalf("valid complete ACL normalization or before-state proof failed: %v", err)
			}
			if digest != "" && sha(body) != digest {
				t.Fatal("protocol representation changed the complete private summary")
			}
			digest = sha(body)
		}
		row, err := redisUser(redisACLDisabledReplyForTest())
		if err != nil || !redisDisabled(row) {
			t.Fatal("actual disabled shape was rejected", err)
		}
		body, err := json.Marshal(row)
		if err != nil || string(body) != `{"channels":"","commands":"-@all","flags":["off"],"keys":"","passwords":[],"selectors":[]}` {
			t.Fatal("disabled complete private summary changed", err)
		}
	})
	t.Run("malformed-fields-and-nil-first-duplicate-reject", func(t *testing.T) {
		resp2, _ := redisACLProtocolRepliesForTest()
		unknown := redisACLDisabledReplyForTest()
		delete(unknown, "channels")
		unknown["unreviewed"] = "private"
		nonstringKey := redisACLDisabledReplyForTest()
		delete(nonstringKey, "channels")
		nonstringKey[1] = ""
		badFlags := redisACLDisabledReplyForTest()
		badFlags["flags"] = []interface{}{"off", int64(1)}
		nilPasswords := redisACLDisabledReplyForTest()
		nilPasswords["passwords"] = nil
		typedNilPasswords := redisACLDisabledReplyForTest()
		typedNilPasswords["passwords"] = []interface{}(nil)
		badSelector := redisACLDisabledReplyForTest()
		badSelector["selectors"] = []interface{}{map[interface{}]interface{}{"commands": "-@all", "keys": "", "unreviewed": ""}}
		duplicateSelector := redisACLDisabledReplyForTest()
		duplicateSelector["selectors"] = []interface{}{[]interface{}{"commands", nil, "commands", "-@all", "keys", ""}}
		nilSelector := redisACLDisabledReplyForTest()
		nilSelector["selectors"] = []interface{}{nil}
		badSelectorValue := redisACLDisabledReplyForTest()
		badSelectorValue["selectors"] = []interface{}{map[interface{}]interface{}{"commands": "-@all", "keys": []interface{}{}, "channels": ""}}
		nilFirstDuplicate := append([]interface{}{"flags", nil}, resp2[:2]...)
		nilFirstDuplicate = append(nilFirstDuplicate, resp2[4:]...)
		for _, test := range []struct {
			name  string
			value any
		}{
			{"nil-root", nil}, {"typed-nil-root", map[interface{}]interface{}(nil)},
			{"unsupported-object", map[string]any{}}, {"odd-pairs", resp2[:len(resp2)-1]},
			{"nil-first-duplicate", nilFirstDuplicate}, {"unknown-field", unknown}, {"nonstring-key", nonstringKey},
			{"nonstr-flags", badFlags}, {"nil-passwords", nilPasswords}, {"typed-nil-passwords", typedNilPasswords},
			{"unknown-selector-field", badSelector}, {"nil-first-selector-duplicate", duplicateSelector},
			{"nil-selector", nilSelector}, {"bad-selector-value", badSelectorValue},
		} {
			if _, err := redisUser(test.value); !errors.Is(err, ErrOwnedEvidence) {
				t.Fatalf("malformed %s did not reject: %v", test.name, err)
			}
		}
	})
	t.Run("disabled-check-keeps-off-allcommands-and-empty-passwords", func(t *testing.T) {
		for _, test := range []struct {
			name, field string
			value       any
		}{
			{"still-on", "flags", []interface{}{"off", "on"}},
			{"no-off", "flags", []interface{}{"nopass"}},
			{"command-rights", "commands", "+@all"},
			{"password-remains", "passwords", []interface{}{"private-hash"}},
			{"selector-remains", "selectors", []interface{}{map[interface{}]interface{}{"commands": "+@all", "keys": "~*", "channels": "&*"}}},
		} {
			reply := redisACLDisabledReplyForTest()
			reply[test.field] = test.value
			row, err := redisUser(reply)
			if err != nil || redisDisabled(row) {
				t.Fatalf("valid but non-disabled %s became exclusion proof: %v", test.name, err)
			}
		}
	})
}
