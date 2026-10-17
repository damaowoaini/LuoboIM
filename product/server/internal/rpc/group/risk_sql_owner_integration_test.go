package group

import (
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/testutil/riskmysql"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"
	"go.mongodb.org/mongo-driver/bson"
)

func TestRoleEffectsOrdinaryOwnerRPCSQLRecoveryAndMongoCommit(t *testing.T) {
	f := riskmysql.Open(t)
	ctx, s, db, _ := roleEffectsFixture(t, nil, nil)
	allEffects := func() int64 {
		n, err := db.Collection(groupeffects.Collection).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": groupeffects.SchemaID}})
		if err != nil {
			t.Fatal("effect inventory unavailable")
		}
		return n
	}
	actor := statusActor(ctx, "owner")
	if authverify.IsAppManagerUid(actor) || roleRow(t, ctx, db, "g", "owner").RoleLevel != constant.GroupOwner {
		t.Fatal("actual ordinary owner fixture acquired manager bypass or lost ownership")
	}
	f.ReplacePolicy(t, map[string]string{riskcontrol.RiskControlEnabledKey: "true", riskcontrol.RiskDisableGroupActionKey: "true", riskcontrol.RiskControlStartKey: "00:00", riskcontrol.RiskControlEndKey: "00:00"})
	config.Config.Mysql = f.WrongConfig()
	req := &pbgroup.TransferGroupOwnerReq{GroupID: "g", OldOwnerUserID: "owner", NewOwnerUserID: "member"}
	if resp, err := s.TransferGroupOwner(actor, req); resp != nil || err == nil || err.Error() != "risk control database unavailable" {
		t.Fatal("ordinary owner did not traverse the unavailable SQL risk boundary")
	}
	if allEffects() != 0 || roleRow(t, ctx, db, "g", "owner").RoleLevel != 100 || roleRow(t, ctx, db, "g", "member").RoleLevel != 20 {
		t.Fatal("SQL failure leaked a Mongo role/effect mutation")
	}
	config.Config.Mysql = f.Config()
	wait := time.NewTimer(1100 * time.Millisecond)
	defer wait.Stop()
	select {
	case <-wait.C:
	case <-ctx.Done():
		t.Fatal("real SQL recovery budget expired")
	}
	if resp, err := s.TransferGroupOwner(actor, req); resp != nil || !errs.ErrNoPermission.Is(err) {
		t.Fatal("restored blocking SQL policy did not reject the real owner RPC")
	}
	if allEffects() != 0 || roleRow(t, ctx, db, "g", "owner").RoleLevel != 100 {
		t.Fatal("blocked SQL policy changed Mongo")
	}
	f.ReplacePolicy(t, map[string]string{riskcontrol.RiskControlEnabledKey: "true", riskcontrol.RiskDisableGroupActionKey: "false"})
	if resp, err := s.TransferGroupOwner(actor, req); err != nil || resp == nil {
		t.Fatal("ordinary-owner RPC did not recover through real SQL to Mongo")
	}
	rows := roleEffects(t, ctx, db)
	if allEffects() != 1 || len(rows) != 1 || rows[0].Producer != "group-role-v1" || rows[0].GroupID != "g" || rows[0].Actor != "owner" || roleRow(t, ctx, db, "g", "owner").RoleLevel != 20 || roleRow(t, ctx, db, "g", "member").RoleLevel != 100 {
		t.Fatal("ordinary-owner SQL->Mongo result or durable notice identity differs")
	}
	// No legacy direct notification is required: this asserts real business TX
	// and frozen obligation admission, not unimplemented receiver/vendor delivery.
}
