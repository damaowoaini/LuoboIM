package authverify

import (
	"errors"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

const (
	ServicePurpose   = "luobo-internal-service-v1"
	DelegatedPurpose = "luobo-admin-delegation-v1"
	AdminAudience    = "luobo-admin-http-v1"
)

type Delegation struct {
	Version     int    `json:"version"`
	AdminID     string `json:"adminID"`
	SessionHash string `json:"sessionHash"`
}
type ClassifiedClaims struct {
	tokenverify.Claims
	Purpose    string      `json:"purpose,omitempty"`
	Delegation *Delegation `json:"delegation,omitempty"`
}

func ParseClassified(token string) (*ClassifiedClaims, error) {
	if token == "" || len(token) > 16384 {
		return nil, errors.New("invalid credential")
	}
	claims := &ClassifiedClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, Secret(), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || parsed == nil || !parsed.Valid || claims.UserID == "" || claims.ExpiresAt == nil {
		return nil, errors.New("invalid credential")
	}
	switch claims.Purpose {
	case "":
		if IsManagerUserID(claims.UserID) {
			return nil, errors.New("legacy shared management credential is disabled")
		}
	case ServicePurpose, DelegatedPurpose:
		if !IsManagerUserID(claims.UserID) || config.Config.AdminDelegation.Issuer == "" || claims.Issuer != config.Config.AdminDelegation.Issuer {
			return nil, errors.New("management credential issuer mismatch")
		}
		if claims.Purpose == DelegatedPurpose && (claims.Delegation == nil || claims.Delegation.Version != 1 || claims.Subject != claims.Delegation.AdminID || claims.PlatformID != 10 || claims.ID == "" || claims.Delegation.AdminID == "" || len(claims.Delegation.AdminID) > 64 || len(claims.Delegation.SessionHash) != 64 || !claims.VerifyAudience(AdminAudience, true)) {
			return nil, errors.New("invalid delegated management binding")
		}
	default:
		return nil, errors.New("unsupported credential purpose")
	}
	return claims, nil
}
