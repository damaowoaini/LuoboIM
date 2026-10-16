package registrationproof

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
)

const publicID = "82b40ec5-561e-45d5-89ce-4ca03f955e8b"
const adminID = "b40ade24-d89a-4906-b4ea-06263ebba060"

func TestProtectedExCompatibilityAndServerOwnedNamespaces(t *testing.T) {
	for _, key := range []string{PublicKey, AdminKey} {
		t.Run(key, func(t *testing.T) {
			old := `{"` + key + `":"` + publicID + `","custom":{"theme":"blue"}}`
			for _, next := range []string{old, `{"other":7}`, ``, `  `, `{"` + key + `":"` + publicID + `","custom":"changed"}`} {
				got, err := ProtectUpdate(old, next)
				if err != nil {
					t.Fatalf("legitimate partial/full write rejected: %v", err)
				}
				var obj map[string]any
				if json.Unmarshal([]byte(got), &obj) != nil || obj[key] != publicID {
					t.Fatal("authoritative proof was erased")
				}
				if next == `{"other":7}` && (obj["other"] != float64(7) || obj["custom"] != nil) {
					t.Fatal("ordinary whole-value replacement semantics changed")
				}
			}
			other := AdminKey
			if key == AdminKey {
				other = PublicKey
			}
			for _, next := range []string{`{"` + key + `":"` + adminID + `"}`, `{"` + key + `":null}`, `{"` + key + `":{}}`, `{"` + other + `":"` + adminID + `"}`, `[]`, `legacy opaque`} {
				if _, err := ProtectUpdate(old, next); err == nil {
					t.Fatal("proof modification/type/substitution/non-JSON erasure accepted")
				}
			}
			for _, old := range []string{"", "legacy opaque", `{}`, `{"custom":"old"}`, `[]`} {
				if _, err := ProtectUpdate(old, `{"`+key+`":"`+publicID+`"}`); err == nil {
					t.Fatal("unmarked user forged a proof")
				}
			}
		})
	}
	for _, pair := range [][2]string{{"", "legacy"}, {"legacy", `{"custom":[1,true,null]}`}, {`{"custom":1}`, "new opaque"}, {"malformed {", ""}, {`{"historical":`, `{"historical":`}} {
		got, err := ProtectUpdate(pair[0], pair[1])
		if err != nil || got != pair[1] {
			t.Fatal("legacy marker-free Ex compatibility changed", err)
		}
	}
}

func TestStructuredExRejectsDuplicateCaseAndUnicodeFoldAliases(t *testing.T) {
	for _, value := range []string{
		`{"luoboRegistrationID":"` + publicID + `","luoboRegistrationID":"` + adminID + `"}`,
		`{"luoboRegistrationID":"` + publicID + `","luobo\u0052egistrationID":"` + publicID + `"}`,
		`{"LUOBOREGISTRATIONID":"` + publicID + `"}`,
		`{"luoboRegiſtrationID":"` + publicID + `"}`,
		`{"luoboAdminProvisioningId":"` + publicID + `"}`,
		`{"ordinary":{"duplicate":1,"duplicate":2}}`,
		`{"luoboRegistrationID":"` + publicID + `"} {}`,
		`{"luoboRegistrationID":"not-a-uuid"}`,
		`{"luoboRegistrationID":"` + publicID + `","luoboAdminProvisioningID":"` + adminID + `"}`,
	} {
		if _, err := InitialMarkers(value); err == nil {
			t.Fatal("ambiguous/malformed initial proof accepted")
		}
		if _, err := ProtectUpdate(`{}`, value); err == nil {
			t.Fatal("ambiguous update accepted")
		}
	}
}

func TestInitialProofCannotBeForgedThroughContextOrFullMap(t *testing.T) {
	ex := `{"luoboRegistrationID":"` + publicID + `"}`
	if CheckCreate(context.Background(), "owner", ex) == nil {
		t.Fatal("full-model insert minted a proof")
	}
	ctx := withInitial(context.Background(), grant{"owner": {PublicKey: publicID}})
	if CheckCreate(ctx, "owner", ex) != nil {
		t.Fatal("exact authenticated initial proof rejected")
	}
	for _, pair := range [][2]string{{"other", ex}, {"owner", `{"luoboAdminProvisioningID":"` + publicID + `"}`}, {"owner", `{"luoboRegistrationID":"` + adminID + `"}`}} {
		if CheckCreate(ctx, pair[0], pair[1]) == nil {
			t.Fatal("initial proof identity/namespace substitution accepted")
		}
	}
	if CheckCreate(context.Background(), "legacy", "plain old Ex") != nil {
		t.Fatal("legacy bootstrap rejected")
	}
	for _, key := range []string{"", "$set", "ex.x", "ex\x00", "user_id", "USER_ID", "_id", "_ID", "EX"} {
		if CheckUpdateMap(map[string]any{key: ex}) == nil {
			t.Fatal("map operator/path/identity alias bypass accepted")
		}
	}
	for _, raw := range []any{nil, 5, map[string]any{PublicKey: publicID}, []string{ex}} {
		if CheckUpdateMap(map[string]any{"ex": raw}) == nil {
			t.Fatal("non-string Ex accepted")
		}
	}
	if CheckUpdateMap(map[string]any{"nickname": "valid", "ex": "legacy"}) != nil {
		t.Fatal("ordinary map rejected")
	}
}

func TestRegisterCallbackLocksOriginalIdentitiesAndBothNamespaces(t *testing.T) {
	s, err := Snapshot([]*sdkws.UserInfo{{UserID: "public", Ex: `{"luoboRegistrationID":"` + publicID + `"}`}, {UserID: "admin", Ex: `{"luoboAdminProvisioningID":"` + adminID + `"}`}, {UserID: "legacy", Ex: "old opaque"}})
	if err != nil {
		t.Fatal(err)
	}
	valid := []*sdkws.UserInfo{{UserID: "admin", Ex: `{"theme":"blue"}`}, {UserID: "public", Ex: ""}, {UserID: "legacy", Ex: `{"custom":2}`}}
	if s.ProtectCallback(valid) != nil {
		t.Fatal("safe callback profile edits rejected")
	}
	for _, u := range valid[:2] {
		m, err := InitialMarkers(u.Ex)
		if err != nil || len(m) != 1 {
			t.Fatal("callback omitted and erased proof")
		}
	}
	for _, changed := range []*sdkws.UserInfo{{UserID: "replacement"}, {UserID: "public", Ex: `{"luoboRegistrationID":"` + adminID + `"}`}, {UserID: "public", Ex: `{"luoboAdminProvisioningID":"` + adminID + `"}`}, {UserID: "public", Ex: `{"LUOBOREGISTRATIONID":"` + publicID + `"}`}} {
		if s.ProtectCallback([]*sdkws.UserInfo{changed, {UserID: "admin"}, {UserID: "legacy"}}) == nil {
			t.Fatal("callback identity/proof substitution accepted")
		}
	}
	legacy, _ := Snapshot([]*sdkws.UserInfo{{UserID: "legacy"}})
	if legacy.ProtectCallback([]*sdkws.UserInfo{{UserID: "legacy", Ex: `{"luoboRegistrationID":"` + publicID + `"}`}}) == nil {
		t.Fatal("callback minted a proof for an unmarked original request")
	}
	for _, users := range [][]*sdkws.UserInfo{nil, {nil}, {{UserID: ""}}, {{UserID: "a"}, {UserID: "a"}}, {{UserID: "bad:id"}}} {
		if _, err := Snapshot(users); err == nil {
			t.Fatal("invalid/ambiguous registration identities accepted")
		}
	}
}
