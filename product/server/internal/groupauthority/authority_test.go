package groupauthority

import (
	"context"
	"errors"
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestGroupAuthorityTargetsAreExactAndOrdered(t *testing.T) {
	for _, input := range [][]Target{nil, {{GroupID: ""}}, {{GroupID: " g"}}, {{GroupID: "g"}, {GroupID: "g", Create: true}}} {
		if _, err := normalizeTargets(input); !errors.Is(err, ErrInvalidScope) {
			t.Fatal("ambiguous group target accepted")
		}
	}
	input := []Target{{GroupID: "z"}, {GroupID: "a", Create: true}, {GroupID: "z"}}
	got, err := normalizeTargets(input)
	if err != nil || len(got) != 2 || got[0].GroupID != "a" || !got[0].Create || got[1].GroupID != "z" || got[1].Create || input[0].GroupID != "z" {
		t.Fatal("group order, creation mode, or immutable input violated")
	}
}

func TestGroupAuthorityRevisionRejectsCorruptionAndOverflow(t *testing.T) {
	cases := []struct {
		name     string
		document bson.M
		n        int64
		exists   bool
		bad      bool
	}{
		{"legacy-absent", bson.M{}, 0, false, false},
		{"server-zero", bson.M{RevisionField: int64(0)}, 0, true, false},
		{"server-revision", bson.M{RevisionField: int64(42)}, 42, true, false},
		{"int32-not-server-int64", bson.M{RevisionField: int32(1)}, 0, true, true},
		{"float", bson.M{RevisionField: float64(1)}, 0, true, true},
		{"string", bson.M{RevisionField: "1"}, 0, true, true},
		{"negative", bson.M{RevisionField: int64(-1)}, 0, true, true},
		{"overflow", bson.M{RevisionField: int64(math.MaxInt64)}, 0, true, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			raw, err := bson.Marshal(item.document)
			if err != nil {
				t.Fatal(err)
			}
			n, exists, err := revision(raw)
			if item.bad {
				if !errors.Is(err, ErrInvalidRevision) {
					t.Fatal("bad revision accepted", err)
				}
				return
			}
			if err != nil || n != item.n || exists != item.exists {
				t.Fatal("revision mismatch", err)
			}
		})
	}
	raw, err := bson.Marshal(bson.D{{Key: RevisionField, Value: int64(1)}, {Key: RevisionField, Value: int64(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := revision(raw); !errors.Is(err, ErrInvalidRevision) {
		t.Fatal("duplicate revision fields accepted")
	}
}

func TestGroupAuthorityScopeCannotBeFabricatedByMetadata(t *testing.T) {
	ctx := context.WithValue(context.Background(), "opuser", "synthetic-manager")
	if !errors.Is(Require(ctx, nil, "g"), ErrInvalidScope) {
		t.Fatal("metadata authorized a writer")
	}
	if !errors.Is(RequireMembers(ctx, nil, "g", []string{"u"}), ErrInvalidScope) {
		t.Fatal("unscoped member write accepted")
	}
	if _, err := NewStore(nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("nil storage accepted")
	}
}

func TestGroupAuthorityReservedFieldsAndCacheNamespace(t *testing.T) {
	for _, field := range []string{RevisionField, "AUTHZ_REVISION", "authz_revision.x", "group_id", "user_id", "$set", "x.y"} {
		if !errors.Is(CheckFields(map[string]any{field: int64(1)}), ErrInvalidScope) {
			t.Fatal("reserved/operator field accepted")
		}
	}
	if CheckFields(map[string]any{"nickname": "synthetic", "ex": "{\"ordinary\":true}"}) != nil {
		t.Fatal("ordinary patch rejected")
	}
	for _, key := range []string{"GROUP_INFO:g", "GROUP_MEMBER_INFO:g-u", "JOIN_GROUPS_KEY:u"} {
		if !cacheKey(key) {
			t.Fatal("existing exact group cache key rejected")
		}
	}
	for _, key := range []string{"", "GROUP_INFO:", "UID_PID_TOKEN_STATUS:u", "GROUP_INFO:g\x00suffix"} {
		if cacheKey(key) {
			t.Fatal("unrelated or malformed cache key accepted")
		}
	}
}

func TestGroupAuthorityTypedTargetsFreezeMemberAndCacheProvenance(t *testing.T) {
	input := []Target{{GroupID: "g", Members: []string{"member"}}}
	ordered, err := normalizeTargets(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Members[0] = "foreign"
	if len(ordered[0].Members) != 1 || ordered[0].Members[0] != "member" {
		t.Fatal("request mutation retargeted frozen member scope")
	}
	scope := &mutationScope{targets: map[string]*targetScope{"g": {members: map[string]bool{"member": true}}}, cacheKeys: make(map[string]struct{})}
	if err := scope.prepareInvalidations(context.Background()); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"GROUP_INFO:g": true, "GROUP_MEMBER_IDS:g": true, "GROUP_MEMBERS_HASH2:g": true, "GROUP_MEMBER_NUM_CACHE:g": true,
		"GROUP_ROLE_LEVEL_MEMBER_IDS:g-20": true, "GROUP_ROLE_LEVEL_MEMBER_IDS:g-60": true, "GROUP_ROLE_LEVEL_MEMBER_IDS:g-100": true,
		"GROUP_MEMBER_INFO:g-member": true, "JOIN_GROUPS_KEY:member": true,
	}
	if len(scope.cacheKeys) != len(allowed) {
		t.Fatal("incomplete exact-target cache keys")
	}
	for key := range scope.cacheKeys {
		if !allowed[key] {
			t.Fatal("unrelated group/member cache key produced")
		}
	}
	if _, err := normalizeTargets([]Target{{GroupID: "g", Members: []string{""}}}); !errors.Is(err, ErrInvalidScope) {
		t.Fatal("empty member widens scope")
	}
	if _, err := normalizeTargets([]Target{{GroupID: "g", Create: true, AllMembers: true}}); !errors.Is(err, ErrInvalidScope) {
		t.Fatal("unknown membership in new group accepted")
	}
}

func TestGroupAuthorityTopologyNeedsWritableTransactionalServer(t *testing.T) {
	for _, item := range []struct {
		name    string
		update  func(bson.M)
		allowed bool
	}{
		{"replica-primary", func(bson.M) {}, true},
		{"mongos-primary", func(h bson.M) { delete(h, "setName"); h["msg"] = "isdbgrid" }, true},
		{"standalone", func(h bson.M) { delete(h, "setName") }, false},
		{"secondary", func(h bson.M) { h["isWritablePrimary"] = false }, false},
		{"no-session-capability", func(h bson.M) { delete(h, "logicalSessionTimeoutMinutes") }, false},
		{"old-wire", func(h bson.M) { h["maxWireVersion"] = int32(6) }, false},
		{"wrong-type", func(h bson.M) { h["logicalSessionTimeoutMinutes"] = "30" }, false},
		{"missing-primary-role", func(h bson.M) { delete(h, "isWritablePrimary") }, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			hello := bson.M{"setName": "synthetic", "isWritablePrimary": true, "maxWireVersion": int32(25), "logicalSessionTimeoutMinutes": int32(30)}
			item.update(hello)
			raw, err := bson.Marshal(hello)
			if err != nil {
				t.Fatal(err)
			}
			if strongTopology(raw) != item.allowed {
				t.Fatal("transaction topology capability mismatch")
			}
		})
	}
}
