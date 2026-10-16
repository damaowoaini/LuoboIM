package service

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"openim-route-service/model"
	"regexp"
	"strings"
)

var ErrAccountNotBound = errors.New("account is not bound")
var ErrCredentials = errors.New("账号或凭证错误")

// Account identifies the verification subject. New keys include the country
// code; old phone-only rows remain a read-only routing fallback.
type Account struct {
	AreaCode    string `json:"areaCode,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	Email       string `json:"email,omitempty"`
}

var digits = regexp.MustCompile(`^[0-9]{6,24}$`)
var areaDigits = regexp.MustCompile(`^\+[0-9]{1,4}$`)

func (a *Account) Normalize() error {
	a.PhoneNumber = strings.TrimSpace(a.PhoneNumber)
	a.Email = strings.TrimSpace(a.Email)
	// Legacy send-code clients placed email in phoneNumber.
	if a.Email == "" && strings.Contains(a.PhoneNumber, "@") {
		a.Email, a.PhoneNumber = a.PhoneNumber, ""
	}
	if (a.Email == "") == (a.PhoneNumber == "") {
		return errors.New("请选择手机号或邮箱")
	}
	if a.Email != "" {
		addr, err := mail.ParseAddress(a.Email)
		if err != nil || addr.Address != a.Email || len(a.Email) > 254 {
			return errors.New("邮箱格式错误")
		}
		a.Email = strings.ToLower(a.Email)
		a.AreaCode = ""
		return nil
	}
	if !digits.MatchString(a.PhoneNumber) {
		return errors.New("手机号格式错误")
	}
	a.AreaCode = strings.TrimSpace(a.AreaCode)
	if a.AreaCode == "" {
		a.AreaCode = "+86"
	}
	if !strings.HasPrefix(a.AreaCode, "+") {
		a.AreaCode = "+" + a.AreaCode
	}
	if !areaDigits.MatchString(a.AreaCode) {
		return errors.New("区号格式错误")
	}
	return nil
}
func (a Account) Key() string {
	if a.Email != "" {
		sum := sha256.Sum256([]byte(strings.ToLower(a.Email)))
		return "email:" + base64.RawURLEncoding.EncodeToString(sum[:]) // 49 chars; legacy VARCHAR(50)
	}
	return "phone:" + a.AreaCode + ":" + a.PhoneNumber
}

// resolveAccount never assigns an old unscoped phone binding to a country. A
// legacy row only locates the tenant; that tenant must authenticate the exact
// areaCode + phoneNumber before any account data or token is returned.
func resolveAccount(routes RouteResolver, account Account) (*model.RouteResponse, error) {
	server, err := routes.GetServerByUserID(account.Key())
	if errors.Is(err, ErrAccountNotBound) && account.PhoneNumber != "" {
		return routes.GetServerByUserID(account.PhoneNumber)
	}
	return server, err
}
