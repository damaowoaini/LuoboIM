package service

import (
	"context"
	"errors"
)

type LoginService struct {
	routeService RouteResolver
	forward      Forwarder
}

func NewLoginService() *LoginService { return &LoginService{NewRouteService(), NewHTTPForwarder()} }

type LoginRequest struct {
	Account
	Password         string `json:"password,omitempty"`
	VerifyCode       string `json:"verifyCode,omitempty"`
	VerificationCode string `json:"verificationCode,omitempty"`
	Platform         int32  `json:"platform"`
	DeviceID         string `json:"deviceID"`
	DeviceModel      string `json:"deviceModel"`
	GoogleAuthCode   string `json:"googleAuthCode,omitempty"`
}

func (s *LoginService) Login(ctx context.Context, req *LoginRequest, clientIP, operationID string) (map[string]interface{}, error) {
	if err := req.Account.Normalize(); err != nil {
		return nil, err
	}
	if req.VerifyCode != "" && req.VerificationCode != "" && req.VerifyCode != req.VerificationCode {
		return nil, errors.New("验证码参数冲突")
	}
	if req.VerifyCode == "" {
		req.VerifyCode = req.VerificationCode
	}
	req.VerificationCode = ""
	if (req.Password == "") == (req.VerifyCode == "") {
		return nil, errors.New("请选择密码或验证码登录")
	}
	if req.Platform <= 0 {
		return nil, errors.New("平台参数错误")
	}
	server, err := resolveAccount(s.routeService, req.Account)
	if err != nil {
		if errors.Is(err, ErrAccountNotBound) {
			return nil, ErrCredentials
		}
		return nil, err
	}
	// Client-supplied ip fields are not part of the DTO. Only the configured
	// trusted-proxy resolver supplies the upstream source address.
	payload := map[string]interface{}{"areaCode": req.AreaCode, "phoneNumber": req.PhoneNumber, "email": req.Email, "password": req.Password, "verifyCode": req.VerifyCode, "platform": req.Platform, "deviceID": req.DeviceID, "deviceModel": req.DeviceModel, "googleAuthCode": req.GoogleAuthCode, "ip": clientIP}
	result, err := s.forward.Post(ctx, server, "/chat/account/login", payload, clientIP, operationID)
	if err != nil {
		return nil, err
	}
	if successful(result) {
		if err := augmentServer(result, server); err != nil {
			return nil, err
		}
	}
	return result, nil
}
