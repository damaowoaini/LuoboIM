package groupeffects

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestGroupEffectsMongoReadinessRejectsLossyOrUnknownCatalog(t *testing.T) {
	for _, mode := range []string{"missing", "capped", "view", "validator", "ttl-index"} {
		t.Run(mode, func(t *testing.T) {
			ctx, _, s, db := effectsMongo(t, nil)
			if mode == "ttl-index" {
				if _, err := db.Collection(Collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "updated_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(60)}); err != nil {
					t.Fatal("owned fixture TTL setup failed", err)
				}
			} else {
				if err := db.Collection(Collection).Drop(ctx); err != nil {
					t.Fatal("owned fixture collection removal failed", err)
				}
				switch mode {
				case "capped":
					if err := db.CreateCollection(ctx, Collection, options.CreateCollection().SetCapped(true).SetSizeInBytes(65536)); err != nil {
						t.Fatal(err)
					}
				case "view":
					if err := db.CreateView(ctx, Collection, "group", []bson.M{}); err != nil {
						t.Fatal(err)
					}
				case "validator":
					if err := db.CreateCollection(ctx, Collection, options.CreateCollection().SetValidator(bson.M{"$jsonSchema": bson.M{"bsonType": "object"}})); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.CheckReady(ctx); err == nil {
				t.Fatal("unsafe/unknown effects catalog accepted")
			}
		})
	}
}
