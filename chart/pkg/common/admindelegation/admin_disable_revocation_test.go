package admindelegation

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/OpenIMSDK/chat/pkg/common/tokenverify"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Only DEL is faulted. SQL, all other Redis operations, the SQL generation
// store and the original revocation worker remain real fixture operations.
// The IM transport is a local stub; no service/IM runtime acceptance is claimed.
type disableRedis struct {
	redis.UniversalClient
	failDelete bool
}

func (r *disableRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	if r.failDelete {
		return redis.NewIntResult(0, errors.New("synthetic Redis DEL failure"))
	}
	return r.UniversalClient.Del(ctx, keys...)
}

type disableIM struct {
	apicall.CallerInterface
	forced string
}

func (*disableIM) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return "synthetic-local-revocation-token", nil
}

func (i *disableIM) ForceOffLine(_ context.Context, id string) error {
	i.forced = id
	return nil
}

func TestAdminDisableRevocationSurvivesEnableAndRedisFailure(t *testing.T) {
	s, id, oldSession := fixture(t)
	ctx := context.Background()
	if e := s.DB.AutoMigrate(&accountstate.Cancellation{}); e != nil {
		t.Fatal("cancellation fixture schema failed")
	}
	oldDelegation, e := s.Issue(ctx, id, oldSession, "configured-service")
	if e != nil {
		t.Fatal("original fixture delegation failed")
	}
	rdb := &disableRedis{UniversalClient: s.Redis, failDelete: true}
	admins := database.NewAdminDatabase(s.DB, rdb)
	if e := admins.UpdateAdmin(ctx, id, map[string]any{"disabled": true}); e != nil {
		t.Fatal("durable disable failed")
	}
	if e := admins.DeleteUserToken(ctx, id); e == nil {
		t.Fatal("synthetic immediate Redis failure did not occur")
	}
	if retained, e := s.Redis.HExists(ctx, "CHAT_UID_TOKEN_STATUS:"+id, oldSession).Result(); e != nil || !retained {
		t.Fatal("failure fixture lost the original cached session")
	}
	if e := admins.UpdateAdmin(ctx, id, map[string]any{"disabled": false}); e != nil {
		t.Fatal("re-enable failed")
	}
	var row admintable.Admin
	if e := s.DB.Where("user_id=?", id).Take(&row).Error; e != nil || row.Disabled {
		t.Fatal("fixture did not reach re-enabled SQL state")
	}
	assertPending := func() {
		t.Helper()
		if pending, e := accountstate.HasPendingCredentials(ctx, s.DB, id); e != nil || !pending {
			t.Fatal("durable revocation barrier was lost")
		}
		if _, e := admins.GetTokens(ctx, id); e == nil {
			t.Fatal("ordinary token authorization bypassed pending revocation")
		}
		if e := admins.CacheToken(ctx, id, "synthetic-blocked-new-session"); e == nil {
			t.Fatal("token issuance bypassed pending revocation")
		}
		if _, _, e := s.AuthorizeSession(ctx, oldSession); e == nil {
			t.Fatal("old administrator session revived while cleanup pending")
		}
		if _, e := s.Issue(ctx, id, oldSession, "configured-service"); e == nil {
			t.Fatal("old session minted a new delegation while cleanup pending")
		}
		if _, _, e := s.Validate(ctx, oldDelegation); e == nil {
			t.Fatal("old delegation revived while cleanup pending")
		}
	}
	assertPending()
	im := &disableIM{}
	worker := &accountstate.CredentialService{Store: accountstate.NewSQLCredentialStore(s.DB), Sessions: accountstate.NewRedisSessions(rdb, im)}
	if e := worker.ProcessUser(ctx, id); e == nil {
		t.Fatal("failed Redis cleanup was marked successful")
	}
	assertPending()
	rdb.failDelete = false
	if e := worker.ProcessUser(ctx, id); e != nil || im.forced != id {
		t.Fatal("original worker did not complete revocation retry")
	}
	if pending, e := accountstate.HasPendingCredentials(ctx, s.DB, id); e != nil || pending {
		t.Fatal("successful generation-bound worker left the job pending")
	}
	tokens, e := admins.GetTokens(ctx, id)
	if e != nil || len(tokens) != 0 {
		t.Fatal("old cached sessions survived successful worker cleanup")
	}
	if _, _, e := s.AuthorizeSession(ctx, oldSession); e == nil {
		t.Fatal("old administrator session survived completed cleanup")
	}
	if _, _, e := s.Validate(ctx, oldDelegation); e == nil {
		t.Fatal("old delegation survived completed cleanup")
	}
	secret := string(s.Secret)
	previousSecret := config.Config.Secret
	config.Config.Secret = &secret
	t.Cleanup(func() { config.Config.Secret = previousSecret })
	newSession, e := tokenverify.CreateToken(id, tokenverify.TokenAdmin, 3600)
	if e != nil || admins.CacheToken(ctx, id, newSession) != nil {
		t.Fatal("new signed administrator session could not be cached after cleanup")
	}
	tokens, e = admins.GetTokens(ctx, id)
	flag, present := tokens[newSession]
	if e != nil || !present || flag != 0 {
		t.Fatal("new cached administrator session was unavailable")
	}
	if actor, _, e := s.AuthorizeSession(ctx, newSession); e != nil || actor != id {
		t.Fatal("new administrator session remained blocked after cleanup")
	}
	newDelegation, e := s.Issue(ctx, id, newSession, "configured-service")
	if e != nil {
		t.Fatal("new session could not issue a delegation after cleanup")
	}
	if actor, _, e := s.Validate(ctx, newDelegation); e != nil || actor != id {
		t.Fatal("new delegation remained blocked after cleanup")
	}
}

func TestAdminDisableOutboxFailureRollsBackAccount(t *testing.T) {
	s, id, session := fixture(t)
	ctx := context.Background()
	const callback = "fixture:reject-admin-disable-outbox"
	if e := s.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "credential_revocations" {
			tx.AddError(errors.New("synthetic revocation queue unavailable"))
		}
	}); e != nil {
		t.Fatal("outbox fault injection could not be installed")
	}
	t.Cleanup(func() { s.DB.Callback().Create().Remove(callback) })
	admins := database.NewAdminDatabase(s.DB, s.Redis)
	if e := admins.UpdateAdmin(ctx, id, map[string]any{"disabled": true}); e == nil {
		t.Fatal("disable committed without its durable revocation job")
	}
	var row admintable.Admin
	if e := s.DB.Where("user_id=?", id).Take(&row).Error; e != nil || row.Disabled {
		t.Fatal("queue failure did not roll back disabled state")
	}
	var jobs int64
	if e := s.DB.Model(&accountstate.CredentialRevocation{}).Where("user_id=?", id).Count(&jobs).Error; e != nil || jobs != 0 {
		t.Fatal("failed transaction left a partial revocation job")
	}
	if actor, _, e := s.AuthorizeSession(ctx, session); e != nil || actor != id {
		t.Fatal("rolled-back disable invalidated the original session")
	}
}
