package api

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
)

var errComplaintReferenceInvalid = errors.New("invalid complaint reference")

type complaintReferences interface {
	TargetName(context.Context, int32, string) (string, error)
	VerifyEvidence(context.Context, string, []string) error
}

type complaintIMCaller interface {
	ImAdminTokenWithDefaultAdmin(context.Context) (string, error)
	FindRegisteredUsers(context.Context, []string) ([]*sdkws.UserInfo, error)
	FindGroupInfo(context.Context, []string) ([]*sdkws.GroupInfo, error)
}

type complaintEvidenceRequest struct {
	OwnerUserID string   `json:"ownerUserID"`
	URLs        []string `json:"urls"`
}

type complaintEvidenceResponse struct {
	Valid bool `json:"valid"`
}

type complaintIMReferences struct {
	caller   complaintIMCaller
	evidence apicall.ApiCaller[complaintEvidenceRequest, complaintEvidenceResponse]
}

func newComplaintReferences() complaintReferences {
	return &complaintIMReferences{
		caller: &apicall.Caller{},
		evidence: apicall.NewApiCaller[complaintEvidenceRequest, complaintEvidenceResponse](
			"/internal/object/verify_evidence", func() string { return config.Config.OpenIMUrl }),
	}
}

func complaintMissingReference(err error) bool {
	code, ok := errs.Unwrap(err).(errs.CodeError)
	if !ok {
		return false
	}
	switch code.Code() {
	case errs.UserIDNotFoundError, errs.GroupIDNotFoundError, errs.RecordNotFoundError:
		return true
	}
	return false
}

func (r *complaintIMReferences) TargetName(ctx context.Context, kind int32, id string) (string, error) {
	if kind != 1 && kind != 2 {
		return "", errComplaintReferenceInvalid
	}
	token, err := r.caller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return "", err
	}
	ctx = mctx.WithApiToken(ctx, token)
	var name string
	var found bool
	if kind == 1 {
		users, err := r.caller.FindRegisteredUsers(ctx, []string{id})
		if err != nil {
			if complaintMissingReference(err) {
				return "", errComplaintReferenceInvalid
			}
			return "", err
		}
		for _, user := range users {
			if user != nil && user.UserID == id {
				name, found = user.Nickname, true
				break
			}
		}
	} else {
		groups, err := r.caller.FindGroupInfo(ctx, []string{id})
		if err != nil {
			if complaintMissingReference(err) {
				return "", errComplaintReferenceInvalid
			}
			return "", err
		}
		for _, group := range groups {
			if group != nil && group.GroupID == id {
				name, found = group.GroupName, true
				break
			}
		}
	}
	if !found {
		return "", errComplaintReferenceInvalid
	}
	return complaintNameSnapshot(name, id), nil
}

func (r *complaintIMReferences) VerifyEvidence(ctx context.Context, owner string, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	token, err := r.caller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return err
	}
	resp, err := r.evidence.Call(mctx.WithApiToken(ctx, token), &complaintEvidenceRequest{OwnerUserID: owner, URLs: urls})
	if err != nil {
		if code, ok := errs.Unwrap(err).(errs.CodeError); ok && code.Code() == errs.ArgsError {
			return errComplaintReferenceInvalid
		}
		return err
	}
	if resp == nil || !resp.Valid {
		return errors.New("unconfirmed complaint evidence verification")
	}
	return nil
}

// Store a bounded authoritative display snapshot. The submitted client name is
// still part of the intent hash, so a retry cannot silently change its payload.
func complaintNameSnapshot(name, fallback string) string {
	var out strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > 255 {
			break
		}
		out.WriteRune(r)
	}
	value := strings.TrimSpace(out.String())
	if value == "" {
		return fallback
	}
	return value
}
