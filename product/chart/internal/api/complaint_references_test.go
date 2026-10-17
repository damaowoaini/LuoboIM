package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
)

type complaintReferenceProbe struct {
	name, owner, target string
	kind                int32
	urls                []string
	targetErr, imageErr error
	targets, images     int
}

func (r *complaintReferenceProbe) TargetName(ctx context.Context, kind int32, id string) (string, error) {
	r.targets++
	r.kind, r.target = kind, id
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 8*time.Second {
		return "", errors.New("missing bounded validation deadline")
	}
	return r.name, r.targetErr
}

func (r *complaintReferenceProbe) VerifyEvidence(_ context.Context, owner string, urls []string) error {
	r.images++
	r.owner, r.urls = owner, append([]string(nil), urls...)
	return r.imageErr
}

func TestComplaintNewIntentRequiresAuthoritativeTargetAndOwnedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		targetErr, imageErr  error
		wantCode, wantImages int
	}{
		{name: "verified", wantCode: 0, wantImages: 1},
		{name: "missing target", targetErr: errComplaintReferenceInvalid, wantCode: 1001},
		{name: "foreign image", imageErr: errComplaintReferenceInvalid, wantCode: 1001, wantImages: 1},
		{name: "target dependency", targetErr: errors.New("private IM token and DB details"), wantCode: errs.ServerInternalError},
		{name: "image dependency", imageErr: errors.New("private IM token and DB details"), wantCode: errs.ServerInternalError, wantImages: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &complaintProbeStore{}
			refs := &complaintReferenceProbe{name: "真实目标", targetErr: tc.targetErr, imageErr: tc.imageErr}
			a := &ComplaintApi{db: store, references: refs}
			body := map[string]any{"clientSubmissionID": "00000000-0000-4000-8000-000000000002", "complaintUserID": "forged", "complaintType": 2, "targetID": "group", "targetName": "forged name", "reason": "其他", "evidence": []string{"https://objects.example.test/owned.png"}}
			code, response := complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
			if code != tc.wantCode || refs.targets != 1 || refs.images != tc.wantImages || strings.Contains(response, "private IM") {
				t.Fatalf("unexpected reference gate: code=%d targets=%d images=%d", code, refs.targets, refs.images)
			}
			if code != 0 {
				if store.created != nil {
					t.Fatal("unverified complaint reached storage")
				}
				return
			}
			if store.created == nil || store.created.ComplaintUserID != "actor" || store.created.TargetName != "真实目标" || refs.owner != "actor" || refs.kind != 2 || refs.target != "group" || len(refs.urls) != 1 {
				t.Fatal("trusted owner, target or evidence batch lost")
			}
			wantHash := complaintPayloadHash(2, "group", "forged name", "其他", "", []string{"https://objects.example.test/owned.png"})
			if store.created.PayloadHash != wantHash {
				t.Fatal("authoritative display name changed the submitted intent hash")
			}
		})
	}
	store := &complaintProbeStore{}
	a := &ComplaintApi{db: store}
	code, _ := complaintCall(t, a.SubmitComplaint, map[string]any{"complaintType": 1, "targetID": "target", "reason": "其他"}, "actor", constant.NormalUser)
	if code != errs.ServerInternalError || store.created != nil {
		t.Fatal("missing reference verifier must fail closed")
	}
}

func TestComplaintReplayPrecedesMutableReferenceValidation(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000003"
	now := time.Now()
	hash := complaintPayloadHash(1, "target", "old name", "其他", "", nil)
	for _, removed := range []*time.Time{nil, &now} {
		store := &complaintProbeStore{value: &table.Complaint{ID: "stable-receipt", ComplaintUserID: "actor", ClientSubmissionID: &id, PayloadHash: hash, RemovedAt: removed}}
		refs := &complaintReferenceProbe{targetErr: errors.New("target deleted and service offline")}
		a := &ComplaintApi{db: store, references: refs}
		body := map[string]any{"clientSubmissionID": id, "complaintType": 1, "targetID": "target", "targetName": "old name", "reason": "其他"}
		code, response := complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
		if code != 0 || !strings.Contains(response, `"complaintID":"stable-receipt"`) || refs.targets != 0 || refs.images != 0 || store.created != nil {
			t.Fatal("committed intent depended on mutable reference state or recreated content")
		}
		body["targetName"] = "changed name"
		code, _ = complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
		if code != 1001 || refs.targets != 0 || store.created != nil {
			t.Fatal("same UUID accepted changed client payload")
		}
		store.value.ComplaintUserID = "other"
		code, response = complaintCall(t, a.SubmitComplaint, body, "actor", constant.NormalUser)
		if code != errs.ServerInternalError || strings.Contains(response, "stable-receipt") || refs.targets != 0 {
			t.Fatal("foreign stored receipt exposed or reached external checks")
		}
	}
	store := &complaintProbeStore{err: errors.New("private SQL")}
	refs := &complaintReferenceProbe{}
	a := &ComplaintApi{db: store, references: refs}
	code, response := complaintCall(t, a.SubmitComplaint, map[string]any{"clientSubmissionID": id, "complaintType": 1, "targetID": "target", "reason": "其他"}, "actor", constant.NormalUser)
	if code != errs.ServerInternalError || strings.Contains(response, "private SQL") || refs.targets != 0 || store.created != nil {
		t.Fatal("failed durable lookup bypassed replay gate")
	}
}

type complaintIMProbe struct {
	users         []*sdkws.UserInfo
	groups        []*sdkws.GroupInfo
	tokenErr, err error
	usersRead     int
	groupsRead    int
	boundToken    string
}

func (p *complaintIMProbe) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return "synthetic-service-purpose", p.tokenErr
}
func (p *complaintIMProbe) FindRegisteredUsers(ctx context.Context, ids []string) ([]*sdkws.UserInfo, error) {
	p.usersRead++
	p.boundToken, _ = ctx.Value(constant.CtxApiToken).(string)
	return p.users, p.err
}
func (p *complaintIMProbe) FindGroupInfo(ctx context.Context, ids []string) ([]*sdkws.GroupInfo, error) {
	p.groupsRead++
	p.boundToken, _ = ctx.Value(constant.CtxApiToken).(string)
	return p.groups, p.err
}

type complaintEvidenceProbe struct {
	request *complaintEvidenceRequest
	resp    *complaintEvidenceResponse
	err     error
	token   string
}

func (p *complaintEvidenceProbe) Call(ctx context.Context, request *complaintEvidenceRequest) (*complaintEvidenceResponse, error) {
	p.request = request
	p.token, _ = ctx.Value(constant.CtxApiToken).(string)
	return p.resp, p.err
}

func TestComplaintIMReferenceRequiresExactTargetAndSafeSnapshot(t *testing.T) {
	for _, kind := range []int32{1, 2} {
		caller := &complaintIMProbe{users: []*sdkws.UserInfo{nil, {UserID: "different", Nickname: "foreign"}}, groups: []*sdkws.GroupInfo{nil, {GroupID: "different", GroupName: "foreign"}}}
		refs := &complaintIMReferences{caller: caller}
		if _, err := refs.TargetName(context.Background(), kind, "target"); !errors.Is(err, errComplaintReferenceInvalid) {
			t.Fatal("nonmatching target considered authoritative")
		}
		caller.users = append(caller.users, &sdkws.UserInfo{UserID: "target", Nickname: "\x00真实\n" + strings.Repeat("🙂", 70)})
		caller.groups = append(caller.groups, &sdkws.GroupInfo{GroupID: "target", GroupName: "\x00真实\n" + strings.Repeat("🙂", 70)})
		name, err := refs.TargetName(context.Background(), kind, "target")
		if err != nil || !utf8.ValidString(name) || len(name) > 255 || strings.ContainsAny(name, "\x00\n") || !strings.HasPrefix(name, "真实") || caller.boundToken != "synthetic-service-purpose" {
			t.Fatal("authoritative name was unbounded or request lacked service token")
		}
		caller.err = errs.ErrRecordNotFound.Wrap("private details")
		if _, err := refs.TargetName(context.Background(), kind, "target"); !errors.Is(err, errComplaintReferenceInvalid) {
			t.Fatal("missing target error not mapped to invalid reference")
		}
		caller.err = errors.New("dependency offline")
		if _, err := refs.TargetName(context.Background(), kind, "target"); err == nil || errors.Is(err, errComplaintReferenceInvalid) {
			t.Fatal("dependency failure misrepresented as missing target")
		}
		caller.tokenErr = errors.New("auth unavailable")
		before := caller.usersRead + caller.groupsRead
		if _, err := refs.TargetName(context.Background(), kind, "target"); err == nil || caller.usersRead+caller.groupsRead != before {
			t.Fatal("token failure did not stop target lookup")
		}
	}
	if complaintNameSnapshot("\n\x00 ", "target") != "target" {
		t.Fatal("blank authoritative name must fall back to exact target ID")
	}
}

func TestComplaintIMEvidenceBindsOwnerAndFailsClosed(t *testing.T) {
	caller := &complaintIMProbe{}
	transport := &complaintEvidenceProbe{resp: &complaintEvidenceResponse{Valid: true}}
	refs := &complaintIMReferences{caller: caller, evidence: transport}
	urls := []string{"https://objects.example.test/object/owned.png"}
	if err := refs.VerifyEvidence(context.Background(), "actor", nil); err != nil || transport.request != nil {
		t.Fatal("empty optional evidence should not call object verifier")
	}
	if err := refs.VerifyEvidence(mctx.WithApiToken(context.Background(), "forged-user-token"), "actor", urls); err != nil || transport.request.OwnerUserID != "actor" || len(transport.request.URLs) != 1 || transport.request.URLs[0] != urls[0] || transport.token != "synthetic-service-purpose" {
		t.Fatal("evidence request lost trusted owner, batch or service purpose")
	}
	for _, tc := range []struct {
		err     error
		invalid bool
	}{
		{errs.ErrArgs.Wrap("private metadata"), true},
		{errors.New("dependency offline"), false},
		{errs.ErrNoPermission.Wrap("proof rejected"), false},
	} {
		transport.err = tc.err
		got := refs.VerifyEvidence(context.Background(), "actor", urls)
		if got == nil || errors.Is(got, errComplaintReferenceInvalid) != tc.invalid {
			t.Fatal("invalid evidence and dependency/proof failures were confused")
		}
	}
	transport.err = nil
	for _, resp := range []*complaintEvidenceResponse{nil, {Valid: false}} {
		transport.resp = resp
		if err := refs.VerifyEvidence(context.Background(), "actor", urls); err == nil || errors.Is(err, errComplaintReferenceInvalid) {
			t.Fatal("unconfirmed success envelope must fail as dependency/protocol error")
		}
	}
	transport.request = nil
	caller.tokenErr = errors.New("token unavailable")
	if err := refs.VerifyEvidence(context.Background(), "actor", urls); err == nil || transport.request != nil {
		t.Fatal("token failure reached evidence transport")
	}
}
