package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// KafkaDeliveryPolicy is an immutable, non-secret configuration snapshot. A
// callback/vendor binding identifies an exact owned runtime adapter/config;
// it is never a caller URL or a credential. Recovery must possess the matching
// adapter rather than substituting whatever config is current after restart.
// This snapshot alone does NOT provide those still-required executable adapters.
type KafkaDeliveryPolicy struct {
	Version string `bson:"version" json:"version"`
	Epoch string `bson:"epoch" json:"epoch"`
	ActivationSHA256 string `bson:"activation_sha256" json:"activationSHA256"`
	ConfigurationSHA256 string `bson:"configuration_sha256" json:"configurationSHA256"`
	Discovery string `bson:"discovery" json:"discovery"`
	BeforeGroupPushBinding string `bson:"before_group_push_binding" json:"beforeGroupPushBinding"`
	BeforeGroupFailureMode string `bson:"before_group_failure_mode" json:"beforeGroupFailureMode"`
	OnlinePushBinding string `bson:"online_push_binding" json:"onlinePushBinding"`
	OnlineFailureMode string `bson:"online_failure_mode" json:"onlineFailureMode"`
	OfflinePushBinding string `bson:"offline_push_binding" json:"offlinePushBinding"`
	OfflineFailureMode string `bson:"offline_failure_mode" json:"offlineFailureMode"`
	Vendor string `bson:"vendor" json:"vendor"`
	VendorBinding string `bson:"vendor_binding" json:"vendorBinding"`
	EncryptionRequired bool `bson:"encryption_required" json:"encryptionRequired"`
}

func (p KafkaDeliveryPolicy) Fingerprint()(string,error){
	if p.Version!="ordinary-kafka-v1" || !validUUID(p.Epoch) || !validSHA(p.ActivationSHA256) || !validSHA(p.ConfigurationSHA256) || p.Discovery!="zookeeper" && p.Discovery!="k8s"{return "",ErrInvalid}
	for _,entry:=range []struct{binding,mode string}{{p.BeforeGroupPushBinding,p.BeforeGroupFailureMode},{p.OnlinePushBinding,p.OnlineFailureMode},{p.OfflinePushBinding,p.OfflineFailureMode}}{
		if entry.binding==""{if entry.mode!=""{return "",ErrInvalid};continue}
		if !validSHA(entry.binding) || entry.mode!="default-stop" && entry.mode!="explicit-stop" && entry.mode!="continue"{return "",ErrInvalid}
	}
	switch p.Vendor{
	case "getui","fcm","jpush":if !validSHA(p.VendorBinding){return "",ErrInvalid}
	case "none":if p.VendorBinding!=""{return "",ErrInvalid}
	default:return "",ErrInvalid // Unknown names must not silently become dummy success.
	}
	body,err:=json.Marshal(p);if err!=nil{return "",ErrInvalid};return hash(body),nil
}

type kafkaPolicyRow struct {
	ID string `bson:"_id"`
	Kind string `bson:"kind"`
	Epoch string `bson:"epoch"`
	SHA256 string `bson:"sha256"`
	Policy KafkaDeliveryPolicy `bson:"policy"`
}

// Non-secret tenant/database identity for the dedicated badge migration.
// The epoch/control binding remains explicit; this is not a readiness proof.
func (s *Store) KafkaBadgeScope()string{
	if s==nil || s.db==nil || !s.membershipRequired{return ""}
	body,_:=json.Marshal([]string{"ordinary-kafka-badge-v1",s.db.Name()});return hash(body)
}

// Called in the FIRST source transaction only. A failed insert/upsert cannot
// leave a committed message without its required frozen configuration.
func (s *Store) freezeKafkaPolicy(ctx context.Context,p KafkaDeliveryPolicy,expected string)error{
	if err:=s.requireTX(ctx);err!=nil{return err}
	digest,err:=p.Fingerprint();if err!=nil || digest!=expected || p.Epoch!=s.epoch || p.ActivationSHA256!=s.activationSHA{return ErrInvalid}
	id:=key(s.epoch,"kafka-policy",digest)
	var existing kafkaPolicyRow
	err=s.db.Collection(Receipts).FindOne(ctx,bson.M{"_id":id}).Decode(&existing)
	if err==nil{
		if existing.ID!=id || existing.Kind!="kafka-policy-v1" || existing.Epoch!=s.epoch || existing.SHA256!=digest || existing.Policy!=p{return ErrConflict};return nil
	}
	if !errors.Is(err,mongo.ErrNoDocuments){return err}
	_,err=s.db.Collection(Receipts).InsertOne(ctx,kafkaPolicyRow{ID:id,Kind:"kafka-policy-v1",Epoch:s.epoch,SHA256:digest,Policy:p})
	return err
}

func (s *Store) readKafkaPolicy(ctx context.Context,digest string)(KafkaDeliveryPolicy,error){
	if !validSHA(digest){return KafkaDeliveryPolicy{},ErrConflict}
	var row kafkaPolicyRow;id:=key(s.epoch,"kafka-policy",digest)
	if err:=s.db.Collection(Receipts).FindOne(ctx,bson.M{"_id":id}).Decode(&row);err!=nil{
		if errors.Is(err,mongo.ErrNoDocuments){return KafkaDeliveryPolicy{},ErrUnprepared};return KafkaDeliveryPolicy{},err
	}
	actual,err:=row.Policy.Fingerprint()
	if err!=nil || actual!=digest || row.ID!=id || row.Kind!="kafka-policy-v1" || row.Epoch!=s.epoch || row.SHA256!=digest || row.Policy.Epoch!=s.epoch || row.Policy.ActivationSHA256!=s.activationSHA{return KafkaDeliveryPolicy{},ErrConflict}
	return row.Policy,nil
}
