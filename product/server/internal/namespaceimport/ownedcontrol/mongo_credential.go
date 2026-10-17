package ownedcontrol

import (
	"context"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// MongoLegacyCredential refers only to a dedicated old owned deployment user.
// UserIDHash is captured from its actual create/usersInfo lifecycle, not guessed
// from the user name. Admin and legacy clients use distinct private credentials.
type MongoLegacyCredential struct{ AuthDB, User, UserIDHash, AdminUser, AdminAuthDB string }
type MongoRevocation struct {
	UserIDHash              string
	BeforeRoleSHA256        string
	AfterRoleSHA256         string
	LiveOldPermissionDenied bool
}

func authenticatedAs(ctx context.Context, client *mongo.Client, user, db string) error {
	var raw bson.Raw
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "connectionStatus", Value: 1}}).Decode(&raw); err != nil {
		return err
	}
	array, ok := raw.Lookup("authInfo", "authenticatedUsers").ArrayOK()
	if !ok {
		return ErrOwnedEvidence
	}
	values, err := array.Values()
	if err != nil || len(values) != 1 {
		return ErrOwnedEvidence
	}
	doc, ok := values[0].DocumentOK()
	if !ok {
		return ErrOwnedEvidence
	}
	u, ok := doc.Lookup("user").StringValueOK()
	if !ok || u != user {
		return ErrOwnedEvidence
	}
	d, ok := doc.Lookup("db").StringValueOK()
	if !ok || d != db {
		return ErrOwnedEvidence
	}
	return nil
}
func mongoUser(ctx context.Context, client *mongo.Client, c MongoLegacyCredential) (bson.Raw, error) {
	var raw bson.Raw
	if err := client.Database(c.AuthDB).RunCommand(ctx, bson.D{{Key: "usersInfo", Value: bson.D{{Key: "user", Value: c.User}, {Key: "db", Value: c.AuthDB}}}, {Key: "showPrivileges", Value: true}, {Key: "showCredentials", Value: false}}).Decode(&raw); err != nil {
		return nil, err
	}
	array, ok := raw.Lookup("users").ArrayOK()
	if !ok {
		return nil, ErrOwnedEvidence
	}
	values, err := array.Values()
	if err != nil || len(values) != 1 {
		return nil, ErrOwnedEvidence
	}
	row, ok := values[0].DocumentOK()
	if !ok {
		return nil, ErrOwnedEvidence
	}
	user, ok := row.Lookup("user").StringValueOK()
	if !ok || user != c.User {
		return nil, ErrOwnedEvidence
	}
	db, ok := row.Lookup("db").StringValueOK()
	if !ok || db != c.AuthDB {
		return nil, ErrOwnedEvidence
	}
	id := row.Lookup("userId")
	if id.Type == 0 || sha(append([]byte{byte(id.Type)}, id.Value...)) != c.UserIDHash {
		return nil, ErrOwnedEvidence
	}
	return row, nil
}
func roleCount(row bson.Raw, key string) (int, string, error) {
	v, ok := row.Lookup(key).ArrayOK()
	if !ok {
		return 0, "", ErrOwnedEvidence
	}
	rows, err := v.Values()
	if err != nil {
		return 0, "", err
	}
	return len(rows), sha(v), nil
}

// RevokeOwnedMongoCredential is an explicit MUTATING migration operation, to be
// called only after the owned launcher admission hold and actual process joins.
// It never runs during Capture/Inspect. Actual usersInfo and a real permission
// denial are both required; auth-disabled fixtures cannot manufacture success.
func RevokeOwnedMongoCredential(ctx context.Context, admin, legacy *mongo.Client, c MongoLegacyCredential) (MongoRevocation, error) {
	if ctx == nil || admin == nil || legacy == nil || !strings.HasPrefix(c.User, "owned_legacy_") || c.User == c.AdminUser || c.AuthDB == "" || !isSHA(c.UserIDHash) || c.AdminUser == "" || c.AdminAuthDB == "" {
		return MongoRevocation{}, ErrOwnedEvidence
	}
	if err := authenticatedAs(ctx, admin, c.AdminUser, c.AdminAuthDB); err != nil {
		return MongoRevocation{}, err
	}
	if err := authenticatedAs(ctx, legacy, c.User, c.AuthDB); err != nil {
		return MongoRevocation{}, err
	}
	var cmdline bson.Raw
	if err := admin.Database("admin").RunCommand(ctx, bson.D{{Key: "getCmdLineOpts", Value: 1}}).Decode(&cmdline); err != nil {
		return MongoRevocation{}, err
	}
	auth, ok := cmdline.Lookup("parsed", "security", "authorization").StringValueOK()
	if !ok || auth != "enabled" {
		return MongoRevocation{}, ErrOwnedEvidence
	}
	before, err := mongoUser(ctx, admin, c)
	if err != nil {
		return MongoRevocation{}, err
	}
	_, beforeSHA, err := roleCount(before, "roles")
	if err != nil {
		return MongoRevocation{}, err
	}
	// No password/DSN is transmitted in argv/logs, and no user is deleted/recreated.
	if err = admin.Database(c.AuthDB).RunCommand(ctx, bson.D{{Key: "updateUser", Value: c.User}, {Key: "roles", Value: bson.A{}}, {Key: "writeConcern", Value: bson.D{{Key: "w", Value: "majority"}, {Key: "j", Value: true}}}}).Err(); err != nil {
		return MongoRevocation{}, err
	}
	after, err := mongoUser(ctx, admin, c)
	if err != nil {
		return MongoRevocation{}, err
	}
	count, afterSHA, err := roleCount(after, "roles")
	if err != nil || count != 0 {
		return MongoRevocation{}, ErrOwnedEvidence
	}
	inherited, _, err := roleCount(after, "inheritedRoles")
	if err != nil || inherited != 0 {
		return MongoRevocation{}, ErrOwnedEvidence
	}
	// A read-only metadata command checks the OLD authenticated connection's real
	// effective authorization. Timeout/connection errors are not permission proof.
	err = legacy.Database(c.AuthDB).RunCommand(ctx, bson.D{{Key: "listCollections", Value: 1}, {Key: "nameOnly", Value: true}}).Err()
	var command mongo.CommandError
	if !errors.As(err, &command) || command.Code != 13 {
		return MongoRevocation{}, ErrOwnedEvidence
	}
	return MongoRevocation{c.UserIDHash, beforeSHA, afterSHA, true}, nil
}
