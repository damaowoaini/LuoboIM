package registrationproof

import (
	"strings"

	"github.com/OpenIMSDK/protocol/sdkws"
)

// RegistrationSnapshot holds original authenticated identities/proofs separately
// from callback-owned pointers. Callback profile edits cannot replace that proof.
type RegistrationSnapshot struct {
	original map[string]string
	allowed  grant
}

func Snapshot(users []*sdkws.UserInfo) (*RegistrationSnapshot, error) {
	if len(users) == 0 {
		return nil, ErrProtected
	}
	s := &RegistrationSnapshot{original: map[string]string{}, allowed: grant{}}
	for _, u := range users {
		if u == nil || u.UserID == "" || strings.Contains(u.UserID, ":") {
			return nil, ErrProtected
		}
		if _, exists := s.original[u.UserID]; exists {
			return nil, ErrProtected
		}
		m, e := InitialMarkers(u.Ex)
		if e != nil {
			return nil, e
		}
		s.original[u.UserID] = u.Ex
		if len(m) > 0 {
			s.allowed[u.UserID] = m
		}
	}
	return s, nil
}
func (s *RegistrationSnapshot) HasMarkers() bool { return s != nil && len(s.allowed) > 0 }
func (s *RegistrationSnapshot) ProtectCallback(users []*sdkws.UserInfo) error {
	if s == nil || len(users) != len(s.original) {
		return ErrProtected
	}
	seen := map[string]bool{}
	for _, u := range users {
		if u == nil || seen[u.UserID] {
			return ErrProtected
		}
		old, exists := s.original[u.UserID]
		if !exists {
			return ErrProtected
		}
		seen[u.UserID] = true
		next, e := ProtectUpdate(old, u.Ex)
		if e != nil {
			return e
		}
		u.Ex = next
	}
	return nil
}
