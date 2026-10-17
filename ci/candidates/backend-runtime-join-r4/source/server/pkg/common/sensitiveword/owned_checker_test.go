package sensitiveword

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
)

type ownedInitialModel struct {
	relation.SensitiveWordModelInterface
}

func (ownedInitialModel) FindAllEnabled(context.Context) ([]*relation.SensitiveWordModel, error) {
	return nil, nil
}

func TestOwnedSensitiveSubscriptionInitialDialUsesOriginalCancellationAndReturnsNoOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	client := redis.NewClient(&redis.Options{MaxRetries: -1, Dialer: func(call context.Context, _, _ string) (net.Conn, error) {
		once.Do(func() { close(entered) })
		<-call.Done()
		return nil, call.Err()
	}})
	defer client.Close()
	var checker *OwnedChecker
	var result error
	go func() { checker, result = NewOwnedSensitiveWordChecker(ctx, ownedInitialModel{}, client); close(done) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual Redis constructor did not reach its original dial boundary")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("initial subscription did not join canceled attempt")
	}
	if checker != nil || result == nil || !errors.Is(result, context.Canceled) {
		t.Fatal("canceled partial subscription returned a live owner", result)
	}
}
