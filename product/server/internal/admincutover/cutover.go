// Package admincutover revokes only proven legacy management credentials. It
// never restores bearer tokens; rollback requires a fresh authorized login.
package admincutover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/golang-jwt/jwt/v4"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/redis/go-redis/v9"
	"sort"
	"strconv"
	"time"
)

type Entry struct {
	UserID     string `json:"userID"`
	PlatformID int    `json:"platformID"`
	TokenHash  string `json:"tokenHash"`
	Status     string `json:"status"`
	key, token string
}
type Plan struct {
	Issuer     string    `json:"issuer"`
	CreatedAt  time.Time `json:"createdAt"`
	State      string    `json:"state"`
	Legacy     []Entry   `json:"legacy"`
	Unresolved int       `json:"unresolved"`
	Removed    int       `json:"removed"`
}

func hash(s string) string { x := sha256.Sum256([]byte(s)); return hex.EncodeToString(x[:]) }
func Inspect(ctx context.Context, rdb redis.UniversalClient, secret, issuer string, identities []string) (*Plan, error) {
	if len(secret) < 32 || issuer == "" {
		return nil, errors.New("explicit tenant issuer and signing key required")
	}
	plan := &Plan{Issuer: issuer, CreatedAt: time.Now().UTC(), State: "inspected", Legacy: []Entry{}}
	seen := map[string]bool{}
	for _, id := range identities {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		for platform, name := range constant.PlatformID2Name {
			key := "UID_PID_TOKEN_STATUS:" + id + ":" + name
			tokens, e := rdb.HGetAll(ctx, key).Result()
			if e != nil {
				return nil, errors.New("management cache unavailable")
			}
			for raw, status := range tokens {
				c := &authverify.ClassifiedClaims{}
				p, e := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithoutClaimsValidation())
				if e != nil || p == nil || !p.Valid || c.UserID != id || c.PlatformID != platform {
					plan.Unresolved++
					continue
				}
				if c.Purpose != "" {
					continue
				}
				plan.Legacy = append(plan.Legacy, Entry{UserID: id, PlatformID: platform, TokenHash: hash(raw), Status: status, key: key, token: raw})
			}
		}
	}
	sort.Slice(plan.Legacy, func(i, j int) bool { return plan.Legacy[i].TokenHash < plan.Legacy[j].TokenHash })
	return plan, nil
}

var remove = redis.NewScript(`if redis.call('HGET',KEYS[1],ARGV[1])==ARGV[2] then return redis.call('HDEL',KEYS[1],ARGV[1]) else return 0 end`)

func Revoke(ctx context.Context, rdb redis.UniversalClient, p *Plan) error {
	if p == nil || p.State != "prepared" {
		return errors.New("persist a prepared plan before revocation")
	}
	for _, entry := range p.Legacy {
		if entry.key == "" || entry.token == "" || hash(entry.token) != entry.TokenHash {
			return errors.New("plan must come from current authenticated inspection")
		}
		n, e := remove.Run(ctx, rdb, []string{entry.key}, entry.token, entry.Status).Int()
		if e != nil {
			p.State = "partial"
			return errors.New("management cache revocation failed")
		}
		p.Removed += n
	}
	p.State = "revoked"
	return nil
}

// MetadataOnly checks that JSON audits cannot become an executable bearer
// restoration source. It is also useful to operators checking retained files.
func MetadataOnly(p *Plan) string {
	return strconv.Itoa(len(p.Legacy)) + " legacy fields; " + strconv.Itoa(p.Unresolved) + " unresolved retained"
}
