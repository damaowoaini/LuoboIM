package service

import (
	"context"
	"errors"
	"openim-route-service/model"
	"time"
)

type RegisterService struct {
	routeService RouteResolver
	forward      Forwarder
	store        RegistrationStore
}

func NewRegisterService(_ ...*VerifyService) *RegisterService {
	return NewRegisterServiceWithSecrets(nil)
}

func NewRegisterServiceWithSecrets(secrets map[string]string) *RegisterService {
	return &RegisterService{NewRouteService(), NewHTTPForwarder(secrets), &SQLRegistrationStore{}}
}

type RegisterRequest struct {
	InvitationCode   string                 `json:"invitationCode"`
	VerificationCode string                 `json:"verificationCode"`
	VerifyCode       string                 `json:"verifyCode"`
	DeviceID         string                 `json:"deviceID"`
	Platform         int32                  `json:"platform"`
	AutoLogin        bool                   `json:"autoLogin"`
	User             map[string]interface{} `json:"user"`
	DeviceModel      string                 `json:"deviceModel"`
}

func userString(user map[string]interface{}, key string) string { s, _ := user[key].(string); return s }
func (s *RegisterService) Register(ctx context.Context, req *RegisterRequest, clientIP, operationID string) (map[string]interface{}, error) {
	account := Account{AreaCode: userString(req.User, "areaCode"), PhoneNumber: userString(req.User, "phoneNumber"), Email: userString(req.User, "email")}
	if err := account.Normalize(); err != nil {
		return nil, err
	}
	if req.Platform <= 0 {
		return nil, errors.New("平台参数错误")
	}
	if req.User == nil {
		req.User = map[string]interface{}{}
	}
	req.User["areaCode"], req.User["phoneNumber"], req.User["email"] = account.AreaCode, account.PhoneNumber, account.Email
	if _, err := resolveAccount(s.routeService, account); err == nil {
		return nil, errors.New("无法完成该账户的注册操作")
	} else if !errors.Is(err, ErrAccountNotBound) {
		return nil, err
	}
	if req.VerifyCode != "" && req.VerificationCode != "" && req.VerifyCode != req.VerificationCode {
		return nil, errors.New("验证码参数不一致")
	}
	if req.VerifyCode == "" {
		req.VerifyCode = req.VerificationCode
	}
	attempt, err := s.store.Prepare(ctx, account.Key(), req.InvitationCode)
	if err != nil {
		return nil, err
	}
	server := attempt.Server
	if configured, ok := s.forward.(interface{ RegistrationConfigured(string) bool }); ok && !configured.RegistrationConfigured(server.ServerID) {
		if attempt.Fresh {
			_ = s.store.Abort(ctx, attempt.Intent.RegistrationID)
		}
		return nil, errors.New("该租户尚未配置安全注册通道")
	}
	signed := registrationContext(ctx, attempt.Intent.RegistrationID)
	// Recovery obtains no credentials and never trusts an unscoped account lookup.
	if !attempt.Fresh {
		recovered, err := s.recoverRegistration(signed, attempt, clientIP, operationID)
		if err != nil {
			return nil, ErrRegistrationPending
		}
		if recovered {
			return nil, ErrRegistrationRecovered
		}
	}
	payload := map[string]interface{}{"invitationCode": req.InvitationCode, "verifyCode": req.VerifyCode, "deviceID": req.DeviceID, "platform": req.Platform, "autoLogin": req.AutoLogin, "user": req.User, "deviceModel": req.DeviceModel, "ip": clientIP}
	result, err := s.forward.Post(signed, server, "/chat/account/register", payload, clientIP, operationID)
	if err != nil {
		return nil, ErrRegistrationPending
	}
	if !successful(result) {
		// Even an HTTP-completed error can follow the chart account/OTP/receipt
		// commit (for example, subsequent IM provisioning failed). Never discard
		// the only registration ID or release its quota from an error code alone.
		recovered, recoveryErr := s.recoverRegistration(signed, attempt, clientIP, operationID)
		if recovered && recoveryErr == nil {
			return nil, ErrRegistrationRecovered
		}
		if recoveryErr != nil {
			return nil, ErrRegistrationPending
		}
		// Only a matching negative receipt plus a known pre-commit rejection
		// permits releasing a fresh reservation. Unknown/server failures remain
		// recoverable even when the account is not visible yet.
		if attempt.Fresh && time.Since(attempt.Intent.CreatedAt) < 50*time.Second && definitiveRegistrationRejection(result) {
			if err := s.store.Abort(ctx, attempt.Intent.RegistrationID); err != nil {
				return nil, ErrRegistrationPending
			}
			return result, nil
		}
		return nil, ErrRegistrationPending
	}
	data, _ := result["data"].(map[string]interface{})
	userID, _ := data["userID"].(string)
	requiresLogin, _ := data["requiresLogin"].(bool)
	if userID == "" || requiresLogin {
		recovered, err := s.recoverRegistration(signed, attempt, clientIP, operationID)
		if err != nil || !recovered {
			return nil, ErrRegistrationPending
		}
		return nil, ErrRegistrationRecovered
	}
	if err := s.store.Confirm(ctx, attempt.Intent.RegistrationID, userID); err != nil {
		return nil, ErrRegistrationPending
	}
	if err := s.store.Complete(ctx, attempt.Intent.RegistrationID); err != nil {
		return nil, ErrRegistrationPending
	}
	if err := augmentServer(result, server); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *RegisterService) recoverRegistration(ctx context.Context, a *RegistrationAttempt, ip, operationID string) (bool, error) {
	result, err := s.forward.Post(ctx, a.Server, "/chat/internal/registration/receipt", map[string]string{"registrationID": a.Intent.RegistrationID, "normalizedIdentity": a.Intent.AccountKey}, ip, operationID)
	if err != nil || !successful(result) {
		return false, ErrRegistrationPending
	}
	data, _ := result["data"].(map[string]interface{})
	registered, known := data["registered"].(bool)
	id, _ := data["registrationID"].(string)
	identity, _ := data["normalizedIdentity"].(string)
	if !known || id != a.Intent.RegistrationID || identity != a.Intent.AccountKey {
		return false, ErrRegistrationPending
	}
	if !registered {
		return false, nil
	}
	provisioned, _ := data["imProvisioned"].(bool)
	userID, _ := data["userID"].(string)
	if !provisioned || id != a.Intent.RegistrationID || identity != a.Intent.AccountKey || userID == "" {
		return false, ErrRegistrationPending
	}
	if err := s.store.Confirm(ctx, id, userID); err != nil {
		return false, err
	}
	if err := s.store.Complete(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

func definitiveRegistrationRejection(result map[string]interface{}) bool {
	code, ok := result["errCode"].(float64)
	if !ok {
		return false
	}
	// Chart's documented argument, duplicate identity, OTP, invitation and
	// registration-policy rejections. A signed negative receipt is also required.
	switch code {
	case 1001, 20003, 20004, 20006, 20007, 20008, 20009, 20010, 20011, 20012, 20014, 20017:
		return true
	default:
		return false
	}
}
func (s *RegisterService) GetServerByInviteCode(code string) (*model.RouteResponse, error) {
	return s.routeService.GetServerByInviteCode(code)
}
func (s *RegisterService) CheckUserExists(key string) (bool, error) {
	_, err := s.routeService.GetServerByUserID(key)
	if errors.Is(err, ErrAccountNotBound) {
		return false, nil
	}
	return err == nil, err
}
