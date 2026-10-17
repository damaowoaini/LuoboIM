package apicall

import (
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/serviceauth"
	"github.com/golang-jwt/jwt/v4"
	"net/http"
	"time"
)

const internalPurpose = "luobo-internal-service-v1"

func internalToken(raw string) bool {
	if config.Config.Secret == nil || config.Config.AdminDelegation.Issuer == "" {
		return false
	}
	var c struct {
		jwt.RegisteredClaims
		Purpose string `json:"purpose"`
	}
	p, e := jwt.ParseWithClaims(raw, &c, func(*jwt.Token) (any, error) { return []byte(*config.Config.Secret), nil }, jwt.WithValidMethods([]string{"HS256"}))
	return e == nil && p != nil && p.Valid && c.Purpose == internalPurpose && c.Issuer == config.Config.AdminDelegation.Issuer
}
func addServiceProof(request *http.Request, logicalPath, token string, body []byte) error {
	if !internalToken(token) {
		return nil
	}
	h, err := serviceauth.Sign([]byte(*config.Config.Secret), serviceauth.Internal, request.Method, logicalPath, token, body, time.Now())
	if err != nil {
		return err
	}
	for key, values := range h {
		request.Header[key] = values
	}
	return nil
}
