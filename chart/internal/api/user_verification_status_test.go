package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type verificationStatusStore struct {
	row   *table.UserVerification
	err   error
	reads []string
}

func (s *verificationStatusStore) Take(_ context.Context, id string) (*table.UserVerification, error) {
	s.reads = append(s.reads, id)
	return s.row, s.err
}

func (*verificationStatusStore) CreateOrUpdate(context.Context, *table.UserVerification) error {
	panic("status read must never write verification data")
}

func (*verificationStatusStore) UpdateStatus(context.Context, string, int32, string) error {
	panic("status read must never change verification state")
}

type verificationStatusReply struct {
	ErrCode int                        `json:"errCode"`
	Data    map[string]json.RawMessage `json:"data"`
}

func verificationStatusRoleName(role int32) string {
	if role == constant.AdminUser {
		return "administrator"
	}
	return "owner"
}

func verificationStatusRequest(t *testing.T, store *verificationStatusStore, actor string, role int32, body string) (verificationStatusReply, string) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/verification/status", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if actor != "" {
		SetToken(c, actor, role)
	}
	(&UserVerificationApi{db: store}).GetVerificationStatus(c)
	var reply verificationStatusReply
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("invalid status response: %v", err)
	}
	return reply, w.Body.String()
}

func syntheticVerificationRow(id string, status int32) *table.UserVerification {
	return &table.UserVerification{
		UserID: id, Status: status,
		RealName: "synthetic-private-name", IDCardNumber: "synthetic-not-a-document",
		IDCardFrontURL: "https://verification.fixture.invalid/front",
		IDCardBackURL: "https://verification.fixture.invalid/back", RejectReason: "synthetic-private-review",
	}
}

func requireVerificationStatusOnly(t *testing.T, reply verificationStatusReply, status int32) {
	t.Helper()
	var actual int32
	if reply.ErrCode != 0 || len(reply.Data) != 1 || json.Unmarshal(reply.Data["status"], &actual) != nil || actual != status {
		t.Fatalf("expected only status %d, got code=%d data=%v", status, reply.ErrCode, reply.Data)
	}
}

func TestVerificationStatusOnlyIsMinimalForEveryState(t *testing.T) {
	for _, role := range []int32{constant.NormalUser, constant.AdminUser} {
		for _, status := range []int32{0, 1, 2, 3} {
			t.Run(verificationStatusRoleName(role)+"/"+strconv.Itoa(int(status)), func(t *testing.T) {
				target := "alice"
				if role == constant.AdminUser {
					target = "bob"
				}
				store := &verificationStatusStore{row: syntheticVerificationRow(target, status)}
				reply, raw := verificationStatusRequest(t, store, "alice", role, `{"userID":"bob","statusOnly":true}`)
				requireVerificationStatusOnly(t, reply, status)
				if len(store.reads) != 1 || store.reads[0] != target || strings.Contains(raw, "synthetic-private") || strings.Contains(raw, "verification.fixture.invalid") {
					t.Fatal("status preflight read the wrong identity or exposed private details")
				}
			})
		}
	}
}

func TestVerificationStatusLegacyDetailsAndAdminTargetRemain(t *testing.T) {
	for _, role := range []int32{constant.NormalUser, constant.AdminUser} {
		t.Run(verificationStatusRoleName(role), func(t *testing.T) {
			target := "alice"
			if role == constant.AdminUser {
				target = "bob"
			}
			store := &verificationStatusStore{row: syntheticVerificationRow(target, 1)}
			reply, _ := verificationStatusRequest(t, store, "alice", role, `{"userID":"bob"}`)
			if reply.ErrCode != 0 || len(reply.Data) != 6 || len(store.reads) != 1 || store.reads[0] != target {
				t.Fatal("legacy detail or legitimate administrator target changed")
			}
			want := map[string]string{
				"realName": store.row.RealName, "idCardNumber": store.row.IDCardNumber,
				"idCardFrontURL": store.row.IDCardFrontURL, "idCardBackURL": store.row.IDCardBackURL,
				"rejectReason": store.row.RejectReason,
			}
			for key, expected := range want {
				var actual string
				if json.Unmarshal(reply.Data[key], &actual) != nil || actual != expected {
					t.Fatalf("legacy field %s changed", key)
				}
			}
		})
	}
}

func TestVerificationStatusRecordAbsenceAloneMeansUnverified(t *testing.T) {
	for _, only := range []bool{false, true} {
		t.Run(map[bool]string{false: "details", true: "status-only"}[only], func(t *testing.T) {
			store := &verificationStatusStore{err: errs.Wrap(gorm.ErrRecordNotFound)}
			body, _ := json.Marshal(map[string]any{"userID": "alice", "statusOnly": only})
			reply, _ := verificationStatusRequest(t, store, "alice", constant.NormalUser, string(body))
			requireVerificationStatusOnly(t, reply, 0)
			if len(store.reads) != 1 || store.reads[0] != "alice" {
				t.Fatal("record absence was not checked for the authenticated owner")
			}
		})
	}
}

func TestVerificationStatusStorageFaultsNeverLookUnverified(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  *table.UserVerification
		err  error
	}{
		{"unavailable", nil, errors.New("synthetic-private-storage-detail")},
		{"missing-success-row", nil, nil},
		{"foreign-success-row", syntheticVerificationRow("bob", 1), nil},
	} {
		for _, only := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "details", true: "status-only"}[only], func(t *testing.T) {
				store := &verificationStatusStore{row: tc.row, err: tc.err}
				body, _ := json.Marshal(map[string]any{"userID": "alice", "statusOnly": only})
				reply, raw := verificationStatusRequest(t, store, "alice", constant.NormalUser, string(body))
				if reply.ErrCode == 0 || len(reply.Data) != 0 || strings.Contains(raw, "synthetic-private") || strings.Contains(raw, "verification.fixture.invalid") {
					t.Fatal("storage failure or foreign row became a success or disclosed private data")
				}
				if len(store.reads) != 1 || store.reads[0] != "alice" {
					t.Fatal("storage fault changed lookup identity")
				}
			})
		}
	}
}

func TestVerificationStatusAdmissionRejectsBeforeStorage(t *testing.T) {
	for _, tc := range []struct{ name, actor, body string }{
		{"unauthenticated", "", `{"userID":"alice","statusOnly":true}`},
		{"missing-user", "alice", `{"statusOnly":true}`},
		{"invalid-status-only", "alice", `{"userID":"alice","statusOnly":"true"}`},
		{"invalid-json", "alice", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &verificationStatusStore{row: syntheticVerificationRow("alice", 1)}
			reply, _ := verificationStatusRequest(t, store, tc.actor, constant.NormalUser, tc.body)
			if reply.ErrCode == 0 || len(store.reads) != 0 {
				t.Fatal("invalid or unauthenticated request reached verification storage")
			}
		})
	}
}
