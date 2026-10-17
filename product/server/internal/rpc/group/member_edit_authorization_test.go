package group

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

var memberEditWriteAdmission = errors.New("synthetic writer admission sentinel")

type memberEditDB struct {
	controller.GroupDatabase
	mu                              sync.Mutex
	rows                            map[[2]string]*relation.GroupMemberModel
	cachedRoles                     map[[2]string]int32
	writes, cachedReads, freshReads int
	written                         []*relation.BatchUpdateGroupMember
	readFailure                     error
	malformed                       []*relation.GroupMemberModel
}

func newMemberEditDB() *memberEditDB {
	db := &memberEditDB{rows: map[[2]string]*relation.GroupMemberModel{}}
	for _, group := range []string{"synthetic-g", "synthetic-h"} {
		for user, role := range map[string]int32{"member": 20, "other": 20, "admin": 60, "peer-admin": 60, "owner": 100} {
			db.rows[[2]string{group, user}] = &relation.GroupMemberModel{GroupID: group, UserID: user, RoleLevel: role}
		}
	}
	return db
}

func (d *memberEditDB) read(group string, ids []string, cached bool) ([]*relation.GroupMemberModel, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cached {
		d.cachedReads++
	} else {
		d.freshReads++
	}
	if d.readFailure != nil {
		return nil, d.readFailure
	}
	if d.malformed != nil {
		return d.malformed, nil
	}
	var rows []*relation.GroupMemberModel
	for _, id := range ids {
		key := [2]string{group, id}
		if current := d.rows[key]; current != nil {
			row := *current
			if role, present := d.cachedRoles[key]; cached && present {
				row.RoleLevel = role
			}
			rows = append(rows, &row)
		}
	}
	return rows, nil
}

func (d *memberEditDB) FindGroupMembers(_ context.Context, group string, ids []string) ([]*relation.GroupMemberModel, error) {
	return d.read(group, ids, true)
}

func (d *memberEditDB) FindGroupMembersForPermission(_ context.Context, group string, ids []string) ([]*relation.GroupMemberModel, error) {
	return d.read(group, ids, false)
}

func (d *memberEditDB) UpdateGroupMembers(_ context.Context, rows []*relation.BatchUpdateGroupMember) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writes++
	for _, row := range rows {
		fields := map[string]any{}
		for key, value := range row.Map {
			fields[key] = value
		}
		d.written = append(d.written, &relation.BatchUpdateGroupMember{GroupID: row.GroupID, UserID: row.UserID, Map: fields})
	}
	// Stop exactly at the real writer boundary, before concrete notification
	// delivery. Positive cases prove admission, not notification/Mongo success.
	return memberEditWriteAdmission
}

func memberEditContext(actor string) context.Context {
	return context.WithValue(context.WithValue(context.Background(), constant.OperationID, "synthetic-member-edit"), constant.OpUserID, actor)
}

func memberEditSetup(t *testing.T) {
	t.Helper()
	before := config.Config
	t.Cleanup(func() { config.Config = before })
	config.Config.Manager.UserID = []string{"synthetic-service"}
	config.Config.IMAdmin.UserID = nil
	config.Config.Callback.CallbackBeforeSetGroupMemberInfo.Enable = false
	config.Config.Secret = "synthetic-member-callback-fixture-secret"
}

func memberEdit(group, user string, role *int32, nickname, ex *string) *pbgroup.SetGroupMemberInfo {
	row := &pbgroup.SetGroupMemberInfo{GroupID: group, UserID: user}
	if role != nil {
		row.RoleLevel = wrapperspb.Int32(*role)
	}
	if nickname != nil {
		row.Nickname = wrapperspb.String(*nickname)
	}
	if ex != nil {
		row.Ex = wrapperspb.String(*ex)
	}
	return row
}

func TestSetGroupMemberInfoRejectsOrdinaryRoleElevation(t *testing.T) {
	memberEditSetup(t)
	admin, name := int32(60), "synthetic nickname"
	for _, mixed := range []string{"role-only", "with-nickname", "batch-with-valid-other-group"} {
		t.Run(mixed, func(t *testing.T) {
			db := newMemberEditDB()
			req := &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", &admin, nil, nil)}}
			if mixed == "with-nickname" {
				req.Members[0].Nickname = wrapperspb.String(name)
			}
			if mixed == "batch-with-valid-other-group" {
				req.Members = append([]*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-h", "member", nil, &name, nil)}, req.Members...)
			}
			_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext("member"), req)
			if !errs.ErrNoPermission.Is(err) || db.writes != 0 {
				t.Fatalf("ordinary self elevation reached writer: writes=%d, error=%v", db.writes, err)
			}
		})
	}
}

func TestSetGroupMemberInfoPreservesLegitimateWriteAdmission(t *testing.T) {
	memberEditSetup(t)
	ordinary, admin, name, ex := int32(20), int32(60), "synthetic nickname", `{"fixture":true}`
	cases := []struct {
		name, actor, target string
		role                *int32
		nickname, ex        *string
		want                map[string]any
	}{
		{"ordinary-nickname-even-if-muted", "member", "member", nil, &name, nil, map[string]any{"nickname": name}},
		{"ordinary-ex", "member", "member", nil, nil, &ex, map[string]any{"ex": ex}},
		{"ordinary-same-role-full-write", "member", "member", &ordinary, &name, &ex, map[string]any{"role_level": ordinary, "nickname": name, "ex": ex}},
		{"owner-promote", "owner", "member", &admin, nil, nil, map[string]any{"role_level": admin}},
		{"owner-demote", "owner", "admin", &ordinary, nil, nil, map[string]any{"role_level": ordinary}},
		{"trusted-manager-not-member", "synthetic-service", "member", &admin, nil, nil, map[string]any{"role_level": admin}},
		{"preserve-existing-admin-to-member", "admin", "member", &admin, nil, nil, map[string]any{"role_level": admin}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newMemberEditDB()
			if tc.name == "ordinary-nickname-even-if-muted" {
				db.rows[[2]string{"synthetic-g", "member"}].MuteEndTime = time.Now().Add(time.Hour)
			}
			req := &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", tc.target, tc.role, tc.nickname, tc.ex)}}
			_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext(tc.actor), req)
			if !errors.Is(err, memberEditWriteAdmission) || db.writes != 1 || len(db.written) != 1 || !reflect.DeepEqual(db.written[0].Map, tc.want) {
				t.Fatalf("legitimate exact payload did not reach writer: writes=%d, error=%v", db.writes, err)
			}
		})
	}
}

func TestSetGroupMemberInfoUsesAuthoritativeRoles(t *testing.T) {
	memberEditSetup(t)
	db := newMemberEditDB()
	db.cachedRoles = map[[2]string]int32{{"synthetic-g", "member"}: constant.GroupOwner}
	name := "other member's nickname"
	_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext("member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "other", nil, &name, nil)}})
	if !errs.ErrNoPermission.Is(err) || db.writes != 0 || db.cachedReads != 0 || db.freshReads == 0 {
		t.Fatalf("cached owner role authorized a foreign member edit: writes=%d, cached=%d, fresh=%d, error=%v", db.writes, db.cachedReads, db.freshReads, err)
	}
}

func memberEditCallback(t *testing.T, change func(), response map[string]any) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
		command := strings.TrimPrefix(r.URL.Path, "/")
		mac := hmac.New(sha256.New, []byte(config.Config.Secret))
		mac.Write([]byte(r.Header.Get("X-OpenIM-Callback-Timestamp") + "\n" + r.Header.Get("X-OpenIM-Callback-Nonce") + "\n" + command + "\n"))
		mac.Write(body)
		signature, decodeErr := hex.DecodeString(r.Header.Get("X-OpenIM-Callback-Signature"))
		if err != nil || decodeErr != nil || len(body) > 8192 || len(r.Header.Get("X-OpenIM-Callback-Nonce")) != 32 || !hmac.Equal(mac.Sum(nil), signature) {
			http.Error(w, "synthetic callback rejected", http.StatusUnauthorized)
			return
		}
		calls.Add(1)
		if change != nil {
			change()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	config.Config.Callback.CallbackUrl = server.URL
	config.Config.Callback.CallbackBeforeSetGroupMemberInfo = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
	return calls
}

func TestSetGroupMemberInfoCallbackCannotElevateOrdinary(t *testing.T) {
	memberEditSetup(t)
	calls := memberEditCallback(t, nil, map[string]any{"roleLevel": 60})
	db := newMemberEditDB()
	name := "valid nickname"
	_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext("member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", nil, &name, nil)}})
	if calls.Load() != 1 || !errs.ErrNoPermission.Is(err) || db.writes != 0 {
		t.Fatalf("callback self promotion reached writer: callbacks=%d, writes=%d, error=%v", calls.Load(), db.writes, err)
	}
}

func TestSetGroupMemberInfoRechecksRolesAfterCallback(t *testing.T) {
	for _, scenario := range []string{"owner-demoted", "actor-left", "target-became-owner"} {
		t.Run(scenario, func(t *testing.T) {
			memberEditSetup(t)
			db := newMemberEditDB()
			actor := "owner"
			if scenario == "target-became-owner" {
				actor = "admin"
			}
			calls := memberEditCallback(t, func() {
				db.mu.Lock()
				defer db.mu.Unlock()
				switch scenario {
				case "owner-demoted":
					db.rows[[2]string{"synthetic-g", actor}].RoleLevel = 20
				case "actor-left":
					delete(db.rows, [2]string{"synthetic-g", actor})
				case "target-became-owner":
					db.rows[[2]string{"synthetic-g", "other"}].RoleLevel = 100
				}
			}, map[string]any{})
			name := "valid at first read"
			_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext(actor), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "other", nil, &name, nil)}})
			if calls.Load() != 1 || db.writes != 0 || !errs.ErrNoPermission.Is(err) {
				t.Fatalf("authority changed during callback but writer admitted: writes=%d, error=%v", db.writes, err)
			}
		})
	}
}

func TestSetGroupMemberInfoCallbackPreservesLegalBodyChanges(t *testing.T) {
	memberEditSetup(t)
	calls := memberEditCallback(t, nil, map[string]any{"nickName": "normalized nickname", "ex": `{"normalized":true}`, "roleLevel": 20})
	db := newMemberEditDB()
	name := "original nickname"
	_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext("member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", nil, &name, nil)}})
	want := map[string]any{"nickname": "normalized nickname", "ex": `{"normalized":true}`, "role_level": int32(20)}
	if calls.Load() != 1 || !errors.Is(err, memberEditWriteAdmission) || len(db.written) != 1 || !reflect.DeepEqual(db.written[0].Map, want) {
		t.Fatal("legal callback edit lost its final payload or write admission", err)
	}
}

func TestSetGroupMemberInfoMalformedAuthorityFailsClosed(t *testing.T) {
	for _, shape := range []string{"missing-actor", "nil-row", "wrong-group", "duplicate-row", "unknown-actor-role", "storage-error"} {
		t.Run(shape, func(t *testing.T) {
			memberEditSetup(t)
			db := newMemberEditDB()
			switch shape {
			case "missing-actor":
				delete(db.rows, [2]string{"synthetic-g", "member"})
			case "nil-row":
				db.malformed = []*relation.GroupMemberModel{nil}
			case "wrong-group":
				db.malformed = []*relation.GroupMemberModel{{GroupID: "synthetic-h", UserID: "member", RoleLevel: 20}}
			case "duplicate-row":
				row := db.rows[[2]string{"synthetic-g", "member"}]
				db.malformed = []*relation.GroupMemberModel{row, row}
			case "unknown-actor-role":
				db.rows[[2]string{"synthetic-g", "member"}].RoleLevel = 5
			case "storage-error":
				db.readFailure = errors.New("synthetic permission read failure")
			}
			name := "no write"
			_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext("member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", nil, &name, nil)}})
			if err == nil || db.writes != 0 {
				t.Fatal("malformed authority reached writer", err)
			}
			if shape != "storage-error" && !errs.ErrNoPermission.Is(err) {
				t.Fatal("malformed authority was not a permission rejection", err)
			}
		})
	}
}

func TestGroupMemberEditIdentityCannotBeRetargeted(t *testing.T) {
	for _, change := range []string{"actor", "group", "member", "append", "nil"} {
		t.Run(change, func(t *testing.T) {
			ctx := memberEditContext("member")
			req := &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", nil, nil, nil)}}
			identity, err := captureGroupMemberEdit(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "actor":
				ctx = memberEditContext("synthetic-service")
			case "group":
				req.Members[0].GroupID = "synthetic-h"
			case "member":
				req.Members[0].UserID = "other"
			case "append":
				req.Members = append(req.Members, memberEdit("synthetic-h", "member", nil, nil, nil))
			case "nil":
				req.Members[0] = nil
			}
			if err := identity.check(ctx, req); !errs.ErrNoPermission.Is(err) {
				t.Fatal("callback edit identity was not locked", err)
			}
		})
	}
}

func TestSetGroupMemberInfoInvalidActorOrRequestNeverWrites(t *testing.T) {
	for _, shape := range []string{"nil-context", "empty-actor", "nil-request", "nil-member", "empty-group", "empty-member", "duplicate-edit"} {
		t.Run(shape, func(t *testing.T) {
			memberEditSetup(t)
			db := newMemberEditDB()
			ctx := memberEditContext("member")
			req := &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", nil, nil, nil)}}
			switch shape {
			case "nil-context":
				ctx = nil
			case "empty-actor":
				ctx = memberEditContext("")
			case "nil-request":
				req = nil
			case "nil-member":
				req.Members[0] = nil
			case "empty-group":
				req.Members[0].GroupID = ""
			case "empty-member":
				req.Members[0].UserID = ""
			case "duplicate-edit":
				req.Members = append(req.Members, req.Members[0])
			}
			_, err := newMemberEditServer(db).SetGroupMemberInfo(ctx, req)
			if err == nil || db.writes != 0 {
				t.Fatal("invalid actor/request reached writer", err)
			}
		})
	}
}

func TestSetGroupMemberInfoInvalidRoleErrorContract(t *testing.T) {
	for _, role := range []int32{100, 0, -1, 42} {
		t.Run(stringRoleForMemberEditTest(role), func(t *testing.T) {
			memberEditSetup(t)
			db := newMemberEditDB()
			_, err := newMemberEditServer(db).SetGroupMemberInfo(memberEditContext("owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("synthetic-g", "member", &role, nil, nil)}})
			if db.writes != 0 {
				t.Fatal("invalid role reached writer")
			}
			if role == 100 {
				if !errs.ErrNoPermission.Is(err) {
					t.Fatal("owner assignment lost the existing permission error", err)
				}
			} else if !errs.ErrArgs.Is(err) {
				t.Fatal("invalid enum lost the existing argument error", err)
			}
		})
	}
}

func stringRoleForMemberEditTest(role int32) string {
	switch role {
	case 100:
		return "owner-forbidden"
	case 0:
		return "zero-argument-error"
	case -1:
		return "negative-argument-error"
	default:
		return "unknown-argument-error"
	}
}
