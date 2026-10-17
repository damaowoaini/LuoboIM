package msgtransfer

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
 "github.com/openimsdk/open-im-server/v3/pkg/common/config"
 "github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
 "github.com/redis/go-redis/v9"
 "go.mongodb.org/mongo-driver/mongo"
)

type ownedAliasTokenFixture struct {cache.MsgModel}
func ownedAliasInputFixture(t *testing.T,name string,value any)aliasvendor.OwnedInputFile{
 t.Helper();directory:=t.TempDir();if err:=os.Chmod(directory,0o700);err!=nil{t.Fatal(err)}
 raw,err:=json.Marshal(value);if err!=nil{t.Fatal(err)};file:=filepath.Join(directory,name)
 if err:=os.WriteFile(file,raw,0o600);err!=nil{t.Fatal(err)};sum:=sha256.Sum256(raw)
 return aliasvendor.OwnedInputFile{Path:file,ExpectedSHA256:hex.EncodeToString(sum[:])}
}
func ownedAliasRegistryFixture(t *testing.T)(*mongo.Database,*redis.Client,aliasvendor.OwnedOfflinePolicyRegistry){
 t.Helper();old:=config.Config;t.Cleanup(func(){config.Config=old})
 config.Config.Envs.Discovery="zookeeper";config.Config.Push.Enable="getui"
 config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable=false
 config.Config.Callback.CallbackOnlinePush.Enable=false;config.Config.Callback.CallbackOfflinePush.Enable=false
 config.Config.Push.GeTui.PushUrl="https://synthetic.invalid/v2/synthetic-app"
 config.Config.Push.GeTui.ChannelID="synthetic-channel";config.Config.Push.GeTui.ChannelName="synthetic-channel-name"
 client,err:=mongo.NewClient();if err!=nil{t.Fatal(err)}
 // NewClient without Connect performs no network. Disconnect is still
 // checked, accepting only the driver's explicit not-connected result.
 t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),2*time.Second);defer cancel();if err:=client.Disconnect(ctx);err!=nil && !errors.Is(err,mongo.ErrClientDisconnected){t.Error("unused local Mongo client cleanup failed")}})
 rdb:=redis.NewClient(&redis.Options{Addr:"127.0.0.1:1"})
 t.Cleanup(func(){if err:=rdb.Close();err!=nil{t.Error("unused local Redis client cleanup failed")}})
 db:=client.Database("synthetic_owned_alias_runtime")
 epoch,activation:="6abaf9cc-1bfd-4a12-9dd3-d2a802f052e1",strings.Repeat("a",64)
 seed,err:=FreezeOwnedKafkaPolicy(db.Name(),epoch,activation);if err!=nil{t.Fatal(err)}
 registry:=aliasvendor.OwnedOfflinePolicyRegistry{Version:"ordinary-owned-offline-policies-v1",Database:db.Name(),Epoch:epoch,ActivationSHA256:activation}
 for i,vendor:=range []string{"getui","jpush","none"}{
  policy:=seed;policy.Vendor=vendor;policy.VendorBinding=""
  bindingSeed:=seed.ConfigurationSHA256;if i!=0{bindingSeed,_=kafkaOwnedDigest([]string{"synthetic original retained configuration",vendor})}
  entry:=aliasvendor.OwnedOfflinePolicySpec{BindingSeedSHA256:bindingSeed}
  if vendor!="none"{
   endpoint:=config.Config.Push.GeTui.PushUrl;if vendor=="jpush"{endpoint="https://synthetic.invalid/v3/push"}
   document:=map[string]any{"version":"ordinary-alias-provider-input-v1","vendor":vendor,"endpoint":endpoint,"appKey":"synthetic-app-key","masterSecret":"synthetic-test-secret","channelID":config.Config.Push.GeTui.ChannelID,"channelName":config.Config.Push.GeTui.ChannelName,"pushIntent":"intent:#Intent;end","production":config.Config.IOSPush.Production}
   input:=ownedAliasInputFixture(t,vendor+".json",document)
   transport,err:=aliasvendor.NewOwnedTransportFromFile(context.Background(),input,bindingSeed);if err!=nil{t.Fatal(err)}
   policy.VendorBinding=transport.Binding();if err:=transport.Close();err!=nil{t.Fatal(err)}
   entry.CredentialPath=input.Path;entry.CredentialSHA256=input.ExpectedSHA256
  }
  policy.ConfigurationSHA256,err=kafkaOwnedDigest([]any{db.Name(),epoch,activation,policy.Discovery,policy.EncryptionRequired,policy.BeforeGroupPushBinding,policy.OnlinePushBinding,policy.OfflinePushBinding,policy.Vendor,policy.VendorBinding});if err!=nil{t.Fatal(err)}
  entry.Policy=policy;entry.Fingerprint,err=policy.Fingerprint();if err!=nil{t.Fatal(err)}
  registry.Entries=append(registry.Entries,entry)
 }
 registry.CurrentPolicySHA256=registry.Entries[0].Fingerprint;return db,rdb,registry
}
func TestOwnedOfflineRuntimeConstructsMixedAliasAndNoneWithoutDBOrProviderDispatch(t *testing.T){
 db,rdb,registry:=ownedAliasRegistryFixture(t)
 input:=ownedAliasInputFixture(t,"registry.json",registry)
 runtime,err:=NewOwnedOrdinaryOfflineRuntime(context.Background(),db,rdb,&ownedAliasTokenFixture{},ownedWorkerReaderFixture{},registry.Epoch,registry.ActivationSHA256,"",input)
 if err!=nil || runtime==nil || runtime.Policy()!=registry.Entries[0].Policy || len(runtime.worker.transports)!=3{t.Fatal("actual mixed-vendor assembly failed before any Start/network",err)}
 for _,entry:=range registry.Entries{if runtime.worker.transports[entry.Fingerprint]==nil{t.Fatal("retained exact adapter was replaced by current vendor")}}
 state,unknown:=runtime.worker.Status();if state!="not-started" || unknown{t.Fatal("constructor silently started recovery")}
 ctx,cancel:=context.WithTimeout(context.Background(),2*time.Second);defer cancel()
 if err:=runtime.Stop(ctx);err!=nil{t.Fatal("unused actual pools did not close",err)}
 drain,err:=runtime.worker.DrainSummary();if err!=nil || len(drain.Policies)!=3 || !drain.WorkerJoined || !drain.OwnedPoolsClosed{t.Fatal("mixed registry lost managed drain identity",err)}
}
func TestOwnedOfflineRuntimeRejectsMissingPrivateMaterialAndCurrentMetadataMismatch(t *testing.T){
 db,rdb,registry:=ownedAliasRegistryFixture(t)
 input:=ownedAliasInputFixture(t,"registry.json",registry)
 config.Config.Push.GeTui.ChannelID="changed-current-channel"
 if runtime,err:=NewOwnedOrdinaryOfflineRuntime(context.Background(),db,rdb,&ownedAliasTokenFixture{},ownedWorkerReaderFixture{},registry.Epoch,registry.ActivationSHA256,"",input);err==nil || runtime!=nil{t.Fatal("foreign current configuration silently reused retained credentials")}
 config.Config.Push.GeTui.ChannelID="synthetic-channel"
 if err:=os.Remove(registry.Entries[1].CredentialPath);err!=nil{t.Fatal(err)}
 if runtime,err:=NewOwnedOrdinaryOfflineRuntime(context.Background(),db,rdb,&ownedAliasTokenFixture{},ownedWorkerReaderFixture{},registry.Epoch,registry.ActivationSHA256,"",input);err==nil || runtime!=nil{t.Fatal("missing old private material silently dropped a retained obligation")}
 if runtime,err:=NewOwnedOrdinaryOfflineRuntime(nil,db,rdb,&ownedAliasTokenFixture{},ownedWorkerReaderFixture{},registry.Epoch,registry.ActivationSHA256,"",input);err==nil || runtime!=nil{t.Fatal("unowned lifecycle started")}
}
