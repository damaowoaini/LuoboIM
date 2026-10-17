package service

import (
	"context"
	"errors"
)

type PasswordService struct {
	routeService RouteResolver
	forward      Forwarder
}

func NewPasswordService(_ ...*VerifyService) *PasswordService {
	return &PasswordService{NewRouteService(), NewHTTPForwarder()}
}

type ResetPasswordRequest struct {
	Account
	VerifyCode string `json:"verifyCode"`
	Password   string `json:"password"`
	Platform   int32  `json:"platform"`
}

func (s *PasswordService) ResetPassword(ctx context.Context, req *ResetPasswordRequest, clientIP, operationID string) (map[string]interface{}, error) {
	if err := req.Account.Normalize(); err != nil {
		return nil, err
	}
	if req.VerifyCode == "" || req.Password == "" {
		return nil, errors.New("请填写验证码和新密码")
	}
	server, err := resolveAccount(s.routeService, req.Account)
	if err != nil {
		if errors.Is(err, ErrAccountNotBound) {
			return nil, ErrCredentials
		}
		return nil, err
	}
	// The tenant owns code validation, purpose isolation, atomic consumption and
	// post-reset session revocation. No universal forward code is substituted.
	return s.forward.Post(ctx, server, "/chat/account/password/reset", req, clientIP, operationID)
}
