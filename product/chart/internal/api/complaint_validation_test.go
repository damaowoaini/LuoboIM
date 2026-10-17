package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type complaintProbeStore struct {
	table.ComplaintInterface
	created           *table.Complaint
	updates           map[string]any
	value             *table.Complaint
	err               error
	reads             int
	owner, submission string
}

func (s *complaintProbeStore) Create(_ context.Context, value *table.Complaint) error {
	s.created = value
	return s.err
}
func (s *complaintProbeStore) Update(_ context.Context, _ string, fields map[string]any) error {
	s.updates = fields
	return s.err
}
func (s *complaintProbeStore) Take(context.Context, string) (*table.Complaint, error) {
	s.reads++
	return s.value, s.err
}
func (s *complaintProbeStore) FindSubmission(_ context.Context, owner, submission string) (*table.Complaint, error) {
	s.reads++
	s.owner, s.submission = owner, submission
	if s.value == nil && s.err == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return s.value, s.err
}
func (s *complaintProbeStore) Search(context.Context, string, string, *int32, *int32, int32, int32) (int64, []*table.Complaint, error) {
	s.reads++
	return 0, nil, s.err
}

func complaintCall(t *testing.T, fn func(*gin.Context), body any, actor string, role int32) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/complaint/test", strings.NewReader(string(data)))
	SetToken(c, actor, role)
	fn(c)
	var result struct {
		ErrCode int `json:"errCode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal("invalid complaint response")
	}
	return result.ErrCode, w.Body.String()
}

func TestComplaintOwnerAndStoragePrivacy(t *testing.T) {
	store := &complaintProbeStore{}
	a := &ComplaintApi{db: store, references: &complaintReferenceProbe{name: "权威目标"}}
	body := map[string]any{"complaintUserID": "forged", "complaintType": 1, "targetID": "target", "targetName": "目标", "reason": "骚扰或辱骂", "description": "第一行\n第二行", "evidence": []string{"https://objects.example.test/owned.png"}}
	code, _ := complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
	if code != 0 || store.created == nil || store.created.ComplaintUserID != "actor" {
		t.Fatal("complaint owner was not authenticated actor")
	}
	store.err = errors.New("sensitive SQL evidence body and password")
	code, response := complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
	if code == 0 || strings.Contains(response, "sensitive SQL") || strings.Contains(response, "password") {
		t.Fatal("storage failure leaked private data or claimed success")
	}
}

func TestComplaintInvalidInputDoesNotReachStorage(t *testing.T) {
	for index, change := range []map[string]any{
		{"complaintType": -1}, {"targetID": strings.Repeat("x", 65)}, {"reason": strings.Repeat("密", 167)},
		{"description": strings.Repeat("x", 4097)}, {"evidence": []string{"http://unsafe.test/p.png"}},
		{"evidence": []string{"https://user:secret@example.test/p.png"}}, {"evidence": []string{"https://example.test/p.png#fragment"}},
		{"evidence": []string{"https://example.test/p.png", "https://example.test/p.png"}},
		{"description": strings.Repeat("x", 70<<10)},
	} {
		store := &complaintProbeStore{}
		a := &ComplaintApi{db: store}
		body := map[string]any{"complaintType": 1, "targetID": "target", "reason": "其他"}
		for k, v := range change {
			body[k] = v
		}
		code, _ := complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
		if code == 0 || store.created != nil {
			t.Fatalf("invalid complaint reached database at case%d", index)
		}
	}
}

func TestComplaintPaginationAndReadOwnership(t *testing.T) {
	store := &complaintProbeStore{value: &table.Complaint{ID: "record", ComplaintUserID: "other", Description: "private report"}}
	a := &ComplaintApi{db: store}
	for _, body := range []map[string]any{{"pageNumber": 1, "showNumber": 101}, {"pageNumber": -1, "showNumber": 20}, {"pageNumber": 100001, "showNumber": 100}, {"pageNumber": 1, "showNumber": 20, "status": -1}} {
		code, _ := complaintCall(t, a.SearchComplaint, body, "actor", constant.NormalUser)
		if code == 0 || store.reads != 0 {
			t.Fatal("unbounded complaint pagination reached database")
		}
	}
	code, response := complaintCall(t, a.GetComplaintDetail, map[string]any{"id": "record"}, "actor", constant.NormalUser)
	if code != 1002 || strings.Contains(response, "private report") {
		t.Fatal("foreign complaint leaked")
	}
}

func TestComplaintHandlingUsesTrustedAdminActor(t *testing.T) {
	store := &complaintProbeStore{}
	a := &ComplaintApi{db: store}
	body := map[string]any{"id": "record", "status": 1, "handlerID": "forged", "handleResult": "处理说明"}
	code, _ := complaintCall(t, a.HandleComplaint, body, "ordinary", constant.NormalUser)
	if code != 1002 || store.updates != nil {
		t.Fatal("ordinary user handled complaint")
	}
	code, _ = complaintCall(t, a.HandleComplaint, body, "real-admin", constant.AdminUser)
	if code != 0 || store.updates["handler_id"] != "real-admin" {
		t.Fatal("untrusted complaint handler")
	}
	store.updates = nil
	body["status"] = 99
	code, _ = complaintCall(t, a.HandleComplaint, body, "real-admin", constant.AdminUser)
	if code == 0 || store.updates != nil {
		t.Fatal("invalid status accepted")
	}
}

func TestComplaintReceiptIsOwnerScopedAndAdvertisesDurableReplay(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	store := &complaintProbeStore{value: &table.Complaint{ID: "receipt", ComplaintUserID: "actor", ClientSubmissionID: &id}}
	a := &ComplaintApi{db: store}
	body := map[string]any{"clientSubmissionID": id, "complaintUserID": "forged"}
	code, response := complaintCall(t, a.Receipt, body, "actor", constant.NormalUser)
	if code != 0 || store.owner != "actor" || store.submission != id || !strings.Contains(response, `"replaySafe":true`) || !strings.Contains(response, `"complaintID":"receipt"`) {
		t.Fatal("receipt did not preserve trusted ownership or durable replay contract")
	}
	store.err = gorm.ErrRecordNotFound
	code, response = complaintCall(t, a.Receipt, body, "actor", constant.NormalUser)
	if code != 0 || !strings.Contains(response, `"found":false`) || !strings.Contains(response, `"replaySafe":true`) {
		t.Fatal("missing receipt must only allow the identical idempotent request")
	}
	store.err = errors.New("private SQL and evidence payload")
	code, response = complaintCall(t, a.Receipt, body, "actor", constant.NormalUser)
	if code == 0 || strings.Contains(response, "private SQL") || strings.Contains(response, `"replaySafe":true`) {
		t.Fatal("failed receipt lookup exposed data or allowed unsafe replay")
	}
	store.err = nil
	store.value.ComplaintUserID = "other"
	code, response = complaintCall(t, a.Receipt, body, "actor", constant.NormalUser)
	if code == 0 || strings.Contains(response, `"complaintID":"receipt"`) {
		t.Fatal("foreign receipt returned by storage leaked")
	}
	before := store.reads
	code, _ = complaintCall(t, a.Receipt, map[string]any{"clientSubmissionID": "invalid"}, "actor", constant.NormalUser)
	if code == 0 || store.reads != before {
		t.Fatal("invalid receipt key reached storage")
	}
}
