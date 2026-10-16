package service

import (
	"context"
	"errors"
	"openim-route-service/sms"
)

// VerifyConfig preserves SMS adapter configuration for administrative tooling.
// Public verification uses the tenant service as the sole code authority.
type VerifyConfig struct {
	SMSSender   sms.SMS
	CodeLength  int
	ValidTime   int
	SuperCode   string // Deprecated; never accepted by the public route service.
	ForwardCode string // Deprecated; never forwarded or accepted.
}
type VerifyService struct {
	routeService RouteResolver
	forward      Forwarder
}

func NewVerifyService(_ VerifyConfig) *VerifyService {
	return &VerifyService{NewRouteService(), NewHTTPForwarder()}
}

type VerifyRequest struct {
	Account
	UsedFor        int32  `json:"usedFor"`
	VerifyCode     string `json:"verifyCode,omitempty"`
	InvitationCode string `json:"invitationCode,omitempty"`
}

func (s *VerifyService) Resolve(req *VerifyRequest) (*Account, error) {
	if err := req.Account.Normalize(); err != nil {
		return nil, err
	}
	if req.UsedFor < 1 || req.UsedFor > 3 {
		return nil, errors.New("验证码用途错误")
	}
	return &req.Account, nil
}
func (s *VerifyService) Verify(ctx context.Context, req *VerifyRequest, check bool, clientIP, operationID string) (map[string]interface{}, error) {
	if _, err := s.Resolve(req); err != nil {
		return nil, err
	}
	if check && req.VerifyCode == "" {
		return nil, errors.New("请填写验证码")
	}
	var result map[string]interface{}
	path := "/chat/account/code/send"
	if check {
		path = "/chat/account/code/verify"
	}
	server, err := resolveAccount(s.routeService, req.Account)
	if req.UsedFor == 1 {
		// Existing accounts cannot be routed by an invitation into another tenant.
		if err == nil {
			// Do not reveal whether registration is already bound.
			return verificationSendAccepted(), nil
		}
		if !errors.Is(err, ErrAccountNotBound) {
			return nil, err
		}
		server, err = s.routeService.GetServerByInviteCode(req.InvitationCode)
	} else if errors.Is(err, ErrAccountNotBound) && !check {
		// Same send-code outcome for unknown accounts, avoiding account enumeration.
		return verificationSendAccepted(), nil
	}
	if err != nil {
		return nil, ErrCredentials
	}
	payload := map[string]interface{}{"areaCode": req.AreaCode, "phoneNumber": req.PhoneNumber, "email": req.Email, "usedFor": req.UsedFor, "verifyCode": req.VerifyCode, "invitationCode": req.InvitationCode, "ip": clientIP}
	result, err = s.forward.Post(ctx, server, path, payload, clientIP, operationID)
	if err == nil && !check && successful(result) {
		return verificationSendAccepted(), nil
	}
	return result, err
}

func verificationSendAccepted() map[string]interface{} {
	return map[string]interface{}{"errCode": float64(0), "errMsg": "", "errDlt": ""}
}
