package ownedmongo

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/mongo"
)

var ErrTargetIdentity = errors.New("original owned Mongo target identity unavailable")

type TargetValidator interface {
	ValidateOwnedMongoTarget(context.Context, *mongo.Database) error
}
type targetContextKey struct{}
type targetContextBinding struct {
	validator TargetValidator
	conflict  bool
}

// This port is only a source-owned constructor constraint, never a permit.
// Ordinary contexts keep their original behavior; a nested replacement cannot
// discard another owner's validation, even if its interface claims success.
func WithTargetValidator(ctx context.Context, v TargetValidator) context.Context {
	if ctx == nil || v == nil {
		return ctx
	}
	_, bound := ctx.Value(targetContextKey{}).(targetContextBinding)
	return context.WithValue(ctx, targetContextKey{}, targetContextBinding{v, bound})
}
func ValidateTarget(ctx context.Context, db *mongo.Database) error {
	if ctx == nil {
		return nil
	}
	binding, ok := ctx.Value(targetContextKey{}).(targetContextBinding)
	if !ok {
		return nil
	}
	if binding.conflict || binding.validator == nil || db == nil || db.Client() == nil || ctx.Err() != nil {
		return ErrTargetIdentity
	}
	return binding.validator.ValidateOwnedMongoTarget(ctx, db)
}
