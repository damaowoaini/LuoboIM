package unrelation

import (
	"testing"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mw/specialerror"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestMongoNoDocumentsMapsToOpenIMRecordNotFound(t *testing.T) {
	// The user cache unwraps the driver's error before checking its code.
	missing := errs.Unwrap(errs.Wrap(mongo.ErrNoDocuments))
	if code := specialerror.ErrCode(missing); code == nil || !errs.ErrRecordNotFound.Is(code) {
		t.Fatalf("missing Mongo document must map to OpenIM record-not-found, got %v", code)
	}
	disconnected := errs.Unwrap(errs.Wrap(mongo.ErrClientDisconnected))
	if code := specialerror.ErrCode(disconnected); code != nil && errs.ErrRecordNotFound.Is(code) {
		t.Fatal("connection failure must retain its own classification")
	}
}
