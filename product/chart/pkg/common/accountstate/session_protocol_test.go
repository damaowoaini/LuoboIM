package accountstate

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/redis/go-redis/v9"
	"testing"
)

type cleanupRedis struct{ redis.UniversalClient }

func (*cleanupRedis) Del(context.Context, ...string) *redis.IntCmd { return redis.NewIntResult(1, nil) }

type cleanupCaller struct {
	apicall.CallerInterface
	operation string
	forced    bool
}

func (c *cleanupCaller) ImAdminTokenWithDefaultAdmin(ctx context.Context) (string, error) {
	c.operation, _ = ctx.Value(constant.OperationID).(string)
	return "test-token", nil
}
func (c *cleanupCaller) ForceOffLine(ctx context.Context, userID string) error {
	c.forced = ctx.Value(constant.OperationID) == c.operation && c.operation != "" && userID == "u1"
	return nil
}
func TestBackgroundCleanupSuppliesOperationID(t *testing.T) {
	caller := &cleanupCaller{}
	sessions := NewRedisSessions(&cleanupRedis{}, caller)
	if err := sessions.Revoke(context.Background(), "u1"); err != nil || !caller.forced {
		t.Fatal("background cleanup lacks required IM operationID", err)
	}
	if err := sessions.Revoke(context.WithValue(context.Background(), constant.OperationID, "existing-operation"), "u1"); err != nil || caller.operation != "existing-operation" {
		t.Fatal("caller operationID changed")
	}
}
