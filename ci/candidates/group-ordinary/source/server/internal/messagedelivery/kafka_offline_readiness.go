package messagedelivery

import (
	"context"
	"errors"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The owned offline runtime has its own Store instance. It is never shared
// with a second offline worker. A restarted runtime creates a new instance;
// old device attempts still name the drained original instance.
func (s *Store) KafkaOfflineInstanceID()string{if s==nil{return ""};return s.instanceID}

// Read every pending ordinary-v1 policy before starting this managed worker.
// Missing adapters, corrupt ordinary duties and Seq0 are explicit unready states,
// never silently skipped or routed through a current replacement policy.
// Other producer families require their separately declared runtime worker.
// This is not a global producer-readiness claim. No DDL/capacity limit exists.
func (s *Store) RequiredKafkaOfflinePolicies(ctx context.Context)([]string,error){
	if s==nil || ctx==nil || mongo.SessionFromContext(ctx)!=nil{return nil,ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
	if err:=s.CheckKafkaReady(current);err!=nil{return nil,err}
	cursor,err:=s.db.Collection(Effects).Find(current,bson.M{"epoch":s.epoch,"producer_version":"ordinary-kafka-v1","offline_pending":true},options.Find().SetBatchSize(128).SetProjection(bson.M{"policy_sha256":1,"persist":1,"seq":1,"hidden":1,"producer_version":1}))
	if err!=nil{return nil,err}
	seen:=map[string]bool{}
	for cursor.Next(current){
		var row struct{Policy string `bson:"policy_sha256"`;Persist bool `bson:"persist"`;Seq int64 `bson:"seq"`;Hidden bool `bson:"hidden"`}
		if cursor.Decode(&row)!=nil || !validSHA(row.Policy) || !row.Persist || row.Seq<1 || row.Hidden{err=ErrUnprepared;break}
		if _,e:=s.readKafkaPolicy(current,row.Policy);e!=nil{err=e;break};seen[row.Policy]=true
	}
	if err==nil{err=cursor.Err()};closeErr:=cursor.Close(current)
	if err!=nil || closeErr!=nil{return nil,errors.Join(err,closeErr)}
	result:=make([]string,0,len(seen));for policy:=range seen{result=append(result,policy)};sort.Strings(result)
	return result,nil
}
