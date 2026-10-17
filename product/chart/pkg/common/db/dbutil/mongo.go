package dbutil

import (
	"context"
	"fmt"
	"github.com/OpenIMSDK/tools/errs"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func MongoFindOne[T any](ctx context.Context, collection *mongo.Collection, filter any, opts ...*options.FindOneOptions) (*T, error) {
	result := collection.FindOne(ctx, filter, opts...)
	if err := result.Err(); err != nil {
		return nil, errs.Wrap(err)
	}
	var t T
	if err := result.Decode(&t); err != nil {
		return nil, errs.Wrap(err)
	}
	return &t, nil
}

func MongoFindAll[T any](ctx context.Context, collection *mongo.Collection, filter any, opts ...*options.FindOptions) ([]*T, error) {
	cursor, err := collection.Find(ctx, filter, opts...)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	var ts []*T
	if err = cursor.All(ctx, &ts); err != nil {
		return nil, errs.Wrap(err)
	}
	return ts, nil
}

func MongoInsertOne(ctx context.Context, collection *mongo.Collection, document any, opts ...*options.InsertOneOptions) error {
	if _, err := collection.InsertOne(ctx, document, opts...); err != nil {
		return errs.Wrap(err)
	}
	return nil
}

func MongoDeleteOne(ctx context.Context, collection *mongo.Collection, filter any, opts ...*options.DeleteOptions) error {
	_, err := collection.DeleteOne(ctx, filter, opts...)
	if err != nil {
		return errs.Wrap(err)
	}
	return nil
}

func MongoDeleteMany(ctx context.Context, collection *mongo.Collection, filter any, opts ...*options.DeleteOptions) error {
	_, err := collection.DeleteMany(ctx, filter, opts...)
	if err != nil {
		return errs.Wrap(err)
	}
	return nil
}

func MongoUpdateOne(ctx context.Context, collection *mongo.Collection, filter any, update any, opts ...*options.UpdateOptions) error {
	_, err := collection.UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return errs.Wrap(err)
	}
	return nil
}

func MongoFindUpdateOne[T any](ctx context.Context, collection *mongo.Collection, filter any, update any, opts ...*options.FindOneAndUpdateOptions) (*T, error) {
	res := collection.FindOneAndUpdate(ctx, filter, update, opts...)
	if err := res.Err(); err != nil {
		return nil, errs.Wrap(err)
	}
	var t T
	if err := res.Decode(&t); err != nil {
		return nil, errs.Wrap(err)
	}
	return &t, nil
}

func MongoAggregateAll[T any](ctx context.Context, collection *mongo.Collection, pipeline any, opts ...*options.AggregateOptions) ([]*T, error) {
	cursor, err := collection.Aggregate(ctx, pipeline, opts...)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	var ts []*T
	if err = cursor.All(ctx, &ts); err != nil {
		return nil, errs.Wrap(err)
	}
	return ts, nil
}

func MongoID(id string) (primitive.ObjectID, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return objectID, errs.ErrArgs.Wrap(fmt.Sprintf("无效的ID %s", id))
	}
	return objectID, nil
}
