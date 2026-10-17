package messagedelivery

import (
 "errors"
 "reflect"
 "testing"

 "go.mongodb.org/mongo-driver/bson"
)

// Only the existing controlled loopback RS + exclusive nonce DB fixture is
// used. No runtime catalog is prepared or repaired by these startup readers.
func TestOwnedOfflineReadinessMongoRequiresTheExactOriginalPendingPolicy(t *testing.T){
 ctx,s,db,_,_,_,policy:=fcmManifestFixture(t,61)
 actual,err:=s.RequiredKafkaOfflinePolicies(ctx)
 if err!=nil || !reflect.DeepEqual(actual,[]string{policy}){t.Fatal("pending original policy not required at startup",err)}
 deleted,err:=db.Collection(Receipts).DeleteOne(ctx,bson.M{"_id":key(s.epoch,"kafka-policy",policy)})
 if err!=nil || deleted.DeletedCount!=1{t.Fatal("exact owned policy fixture deletion failed",err)}
 if _,err:=s.RequiredKafkaOfflinePolicies(ctx);!errors.Is(err,ErrUnprepared){t.Fatal("missing original policy was inferred from current configuration",err)}
 if n,err:=db.Collection(Receipts).CountDocuments(ctx,bson.M{"_id":key(s.epoch,"kafka-policy",policy)});err!=nil || n!=0{t.Fatal("readiness repaired missing policy",err)}
}
func TestMembershipReaderMongoStartupRejectsMissingPreparedSchemaWithoutDDL(t *testing.T){
 ctx,_,s,db,_:=kafkaFixture(t,nil)
 t.Setenv(HistoryWindowModeEnv,"required");t.Setenv(HistoryWindowEpochEnv,s.epoch);t.Setenv(HistoryWindowActivationEnv,s.activationSHA)
 reader,err:=NewHistoryWindowReaderFromEnvironment(db);if err!=nil || reader==nil{t.Fatal("explicit required reader failed construction",err)}
 if err:=reader.CheckReady(ctx);err!=nil{t.Fatal("prepared reader startup rejected",err)}
 changed,err:=db.Collection(Receipts).DeleteOne(ctx,bson.M{"_id":MembershipSchemaID});if err!=nil || changed.DeletedCount!=1{t.Fatal("exact owned readiness marker not removed",err)}
 if err:=reader.CheckReady(ctx);err==nil{t.Fatal("missing catalog accepted at service startup")}
 if n,err:=db.Collection(Receipts).CountDocuments(ctx,bson.M{"_id":MembershipSchemaID});err!=nil || n!=0{t.Fatal("startup implicitly recreated migration marker",err)}
}
