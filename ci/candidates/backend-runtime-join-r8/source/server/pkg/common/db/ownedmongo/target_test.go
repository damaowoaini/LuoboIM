package ownedmongo

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"testing"
)

type originalTargetProbe struct {
	want    *mongo.Database
	calls   int
	failure error
}

func (p *originalTargetProbe) ValidateOwnedMongoTarget(ctx context.Context, db *mongo.Database) error {
	p.calls++
	if db != p.want {
		return ErrTargetIdentity
	}
	return p.failure
}
func TestOwnedMongoTargetPortPreservesOrdinaryPathAndOriginalClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, e := mongo.NewClient(options.Client())
	if e != nil {
		t.Fatal(e)
	}
	db := client.Database("synthetic_target")
	probe := &originalTargetProbe{want: db}
	t.Run("ordinary-context-does-not-create-a-new-admission-gate", func(t *testing.T) {
		if e := ValidateTarget(ctx, nil); e != nil || probe.calls != 0 {
			t.Fatal("ordinary constructor behavior changed", e)
		}
	})
	t.Run("original-handle-and-real-error-are-not-substituted", func(t *testing.T) {
		bound := WithTargetValidator(ctx, probe)
		if e := ValidateTarget(bound, db); e != nil || probe.calls != 1 {
			t.Fatal("actual original handle did not reach the source validator", e)
		}
		failure := errors.New("synthetic unknown target observation")
		probe.failure = failure
		if e := ValidateTarget(bound, db); !errors.Is(e, failure) || probe.calls != 2 {
			t.Fatal("unknown observation became success", e)
		}
		if e := ValidateTarget(bound, client.Database(db.Name())); !errors.Is(e, ErrTargetIdentity) {
			t.Fatal("leaf changed the original handle")
		}
	})
	t.Run("nested-replacement-cannot-drop-first-validation", func(t *testing.T) {
		other := &originalTargetProbe{want: db}
		bound := WithTargetValidator(WithTargetValidator(ctx, probe), other)
		before := probe.calls
		if e := ValidateTarget(bound, db); !errors.Is(e, ErrTargetIdentity) || probe.calls != before || other.calls != 0 {
			t.Fatal("later port replaced original target identity", e)
		}
	})
}
