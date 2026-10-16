package aliasvendor

import (
 "bytes"
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func privateAliasFixture(t *testing.T,name string,body []byte)OwnedInputFile{
 t.Helper();directory:=t.TempDir();if err:=os.Chmod(directory,0o700);err!=nil{t.Fatal(err)}
 file:=filepath.Join(directory,name);if err:=os.WriteFile(file,body,0o600);err!=nil{t.Fatal(err)}
 return OwnedInputFile{Path:file,ExpectedSHA256:digest(body)}
}
func TestAliasOwnedFileRejectsWrongPinPermissionsLinksAndRotatedBytes(t *testing.T){
 body:=[]byte("synthetic source-owned bytes");input:=privateAliasFixture(t,"private.json",body)
 actual,err:=readOwnedInputFile(input);if err!=nil || !bytes.Equal(actual,body){t.Fatal("exact original private bytes not read",err)}
 wrong:=input;wrong.ExpectedSHA256=strings.Repeat("a",64);if _,err:=readOwnedInputFile(wrong);err==nil{t.Fatal("wrong file fingerprint accepted")}
 if err:=os.Chmod(input.Path,0o644);err!=nil{t.Fatal(err)};if _,err:=readOwnedInputFile(input);err==nil{t.Fatal("public readable input accepted")}
 if err:=os.Chmod(input.Path,0o600);err!=nil{t.Fatal(err)}
 alias:=filepath.Join(filepath.Dir(input.Path),"alias.json")
 if err:=os.Symlink(input.Path,alias);err!=nil{t.Fatal(err)};if _,err:=readOwnedInputFile(OwnedInputFile{Path:alias,ExpectedSHA256:input.ExpectedSHA256});err==nil{t.Fatal("symbolic alias became credential source")}
 if err:=os.Remove(alias);err!=nil{t.Fatal(err)};if err:=os.Link(input.Path,alias);err!=nil{t.Fatal(err)}
 if _,err:=readOwnedInputFile(input);err==nil{t.Fatal("multiply linked input accepted")};if err:=os.Remove(alias);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(input.Path,[]byte("changed private bytes"),0o600);err!=nil{t.Fatal(err)};if _,err:=readOwnedInputFile(input);err==nil{t.Fatal("changed input reused old credential pin")}
 if err:=os.Chmod(filepath.Dir(input.Path),0o755);err!=nil{t.Fatal(err)};if _,err:=readOwnedInputFile(input);err==nil{t.Fatal("nonprivate source directory accepted")}
}
func TestAliasOwnedFileFactoryUsesOnlyExplicitLocalMaterialAndClosesWithoutAuth(t *testing.T){
 document:=ownedDocument{Version:"ordinary-alias-provider-input-v1",Vendor:"getui",Endpoint:"https://synthetic.invalid/v2/synthetic-app",AppKey:"synthetic-key",MasterSecret:"synthetic-secret"}
 raw,err:=json.Marshal(document);if err!=nil{t.Fatal(err)};input:=privateAliasFixture(t,"provider.json",raw)
 t.Setenv("GETUI_APP_KEY","ambient-is-never-read");t.Setenv("JPUSH_MASTER_SECRET","ambient-is-never-read")
 transport,err:=NewOwnedTransportFromFile(context.Background(),input,digest([]byte("synthetic configuration seed")))
 if err!=nil || transport==nil || !validDigest(transport.Binding()) || transport.token!="" || transport.expiresMS!=0{t.Fatal("offline construction consumed ambient discovery or authenticated",err)}
 if err:=transport.Close();err!=nil{t.Fatal(err)}
 if transport.CheckReady(context.Background())==nil{t.Fatal("closed owned pool is ready")}
 cancelled,cancel:=context.WithCancel(context.Background());cancel()
 if value,err:=NewOwnedTransportFromFile(cancelled,input,digest([]byte("seed")));err==nil || value!=nil{t.Fatal("cancelled owner created a credential pool")}
}
func aliasRegistryFixture(t *testing.T)(OwnedOfflinePolicyRegistry,[]byte){
 t.Helper();registry:=OwnedOfflinePolicyRegistry{Version:"ordinary-owned-offline-policies-v1",Database:"synthetic_alias_registry",Epoch:"f28d15d2-78fe-490a-983c-556862637809",ActivationSHA256:strings.Repeat("a",64)}
 for _,vendor:=range []string{"getui","jpush","fcm","none"}{
  policy:=messagedelivery.KafkaDeliveryPolicy{Version:"ordinary-kafka-v1",Epoch:registry.Epoch,ActivationSHA256:registry.ActivationSHA256,Discovery:"zookeeper",ConfigurationSHA256:digest([]byte("frozen original configuration "+vendor)),Vendor:vendor}
  entry:=OwnedOfflinePolicySpec{Policy:policy,BindingSeedSHA256:digest([]byte("original seed "+vendor))}
  if vendor!="none"{entry.Policy.VendorBinding=digest([]byte("original provider material "+vendor));entry.CredentialPath=filepath.Join(t.TempDir(),vendor+".json");entry.CredentialSHA256=digest([]byte("original private file "+vendor))}
  fingerprint,err:=entry.Policy.Fingerprint();if err!=nil{t.Fatal(err)};entry.Fingerprint=fingerprint;registry.Entries=append(registry.Entries,entry)
 }
 registry.CurrentPolicySHA256=registry.Entries[0].Fingerprint;raw,err:=json.Marshal(registry);if err!=nil{t.Fatal(err)};return registry,raw
}
func TestAliasOwnedRegistryPreservesMixedOriginalPoliciesAndExplicitNone(t *testing.T){
 registry,raw:=aliasRegistryFixture(t);input:=privateAliasFixture(t,"registry.json",raw)
 actual,err:=LoadOwnedOfflineRegistry(context.Background(),input,registry.Database,registry.Epoch,registry.ActivationSHA256)
 if err!=nil || len(actual.Entries)!=4 || actual.CurrentPolicySHA256!=registry.CurrentPolicySHA256{t.Fatal("retained mixed provider registry rejected",err)}
 for i,entry:=range actual.Entries{if entry!=registry.Entries[i]{t.Fatal("retained material was rebound to current provider")}}
 // Loading the registry is local data validation, not proof that any old
 // private credential exists or that its supplier/device is reachable.
 if _,err:=LoadOwnedOfflineRegistry(context.Background(),input,"another_database",registry.Epoch,registry.ActivationSHA256);err==nil{t.Fatal("foreign database reused original policy registry")}
}
func TestAliasOwnedRegistryRejectsNestedAmbiguityForeignEpochAndMissingCurrent(t *testing.T){
 registry,raw:=aliasRegistryFixture(t)
 for _,name:=range []string{"duplicate","case","foreign-epoch","missing-current","repeated-entry","credential-on-none"}{t.Run(name,func(t *testing.T){
  copy:=registry;copy.Entries=append([]OwnedOfflinePolicySpec(nil),registry.Entries...);var changed []byte
  switch name{
  case "duplicate":changed=bytes.Replace(raw,[]byte(`"vendor":"getui"`),[]byte(`"vendor":"getui","Vendor":"getui"`),1)
  case "case":changed=bytes.Replace(raw,[]byte(`"credentialPath"`),[]byte(`"CredentialPath"`),1)
  case "foreign-epoch":copy.Entries[0].Policy.Epoch="02fa467a-7428-4b77-9a9c-a00d02b7e12b";copy.Entries[0].Fingerprint,_=copy.Entries[0].Policy.Fingerprint()
  case "missing-current":copy.CurrentPolicySHA256=strings.Repeat("b",64)
  case "repeated-entry":copy.Entries=append(copy.Entries,copy.Entries[0])
  case "credential-on-none":copy.Entries[3].CredentialPath="/synthetic/forbidden.json";copy.Entries[3].CredentialSHA256=strings.Repeat("b",64)
  }
  if changed==nil{var err error;changed,err=json.Marshal(copy);if err!=nil{t.Fatal(err)}}
  if _,err:=decodeOwnedOfflineRegistry(changed,registry.Database,registry.Epoch,registry.ActivationSHA256);err==nil{t.Fatal("ambiguous or foreign retained policy accepted")}
 })}
}
