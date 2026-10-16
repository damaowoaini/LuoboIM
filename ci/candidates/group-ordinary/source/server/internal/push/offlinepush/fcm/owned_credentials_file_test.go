package fcm

import (
 "bytes"
 "context"
 "crypto/rand"
 "crypto/rsa"
 "encoding/json"
 "encoding/pem"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func ownedCredentialFixtureFile(t *testing.T,name string,body []byte)OwnedCredentialFile{
 t.Helper();directory:=t.TempDir();if err:=os.Chmod(directory,0o700);err!=nil{t.Fatal(err)}
 path:=filepath.Join(directory,name);if err:=os.WriteFile(path,body,0o600);err!=nil{t.Fatal(err)}
 return OwnedCredentialFile{Path:path,ExpectedSHA256:frozenHash(body)}
}
func syntheticOwnedCredential(t *testing.T)[]byte{
 t.Helper();key,err:=rsa.GenerateKey(rand.Reader,2048);if err!=nil{t.Fatal("synthetic key generation failed")}
 raw,err:=json.Marshal(map[string]string{"type":"service_account","project_id":"synthetic-owned","client_email":"synthetic@synthetic-owned.iam.gserviceaccount.com","private_key_id":"synthetic-current-key","private_key":string(pem.EncodeToMemory(&pem.Block{Type:"PRIVATE KEY",Bytes:mustHTTPV1PKCS8(t,key)})),"token_uri":httpV1TokenURL})
 if err!=nil{t.Fatal("synthetic account encoding failed")};return raw
}
func TestFCMOwnedFileExactPrivateBytesAndPinAreRequired(t *testing.T){
 body:=[]byte(`{"synthetic":"file-only fixture"}`)
 input:=ownedCredentialFixtureFile(t,"credential.json",body)
 actual,err:=readOwnedCredentialFile(input);if err!=nil || !bytes.Equal(actual,body){t.Fatal("valid exact private bytes rejected",err)}
 wrong:=input;wrong.ExpectedSHA256=frozenHash([]byte("different content"));if _,err:=readOwnedCredentialFile(wrong);err==nil{t.Fatal("wrong deployed pin accepted")}
 for _,mode:=range []os.FileMode{0o644,0o400}{if err:=os.Chmod(input.Path,mode);err!=nil{t.Fatal(err)};if _,err:=readOwnedCredentialFile(input);err==nil{t.Fatal("noncontract credential mode accepted")}}
 if err:=os.Chmod(input.Path,0o600);err!=nil{t.Fatal(err)}
 if err:=os.Chmod(filepath.Dir(input.Path),0o755);err!=nil{t.Fatal(err)};if _,err:=readOwnedCredentialFile(input);err==nil{t.Fatal("nonprivate parent accepted")}
}
func TestFCMOwnedFileSymlinkHardlinkAndChangedMaterialFailClosed(t *testing.T){
 body:=[]byte("synthetic-file-only");input:=ownedCredentialFixtureFile(t,"original.json",body)
 alias:=filepath.Join(filepath.Dir(input.Path),"alias.json")
 if err:=os.Symlink(input.Path,alias);err!=nil{t.Fatal(err)}
 if _,err:=readOwnedCredentialFile(OwnedCredentialFile{Path:alias,ExpectedSHA256:input.ExpectedSHA256});err==nil{t.Fatal("symlink acquired private credential identity")}
 if err:=os.Remove(alias);err!=nil{t.Fatal(err)}
 if err:=os.Link(input.Path,alias);err!=nil{t.Fatal(err)};if _,err:=readOwnedCredentialFile(input);err==nil{t.Fatal("multiply linked credential accepted")}
 if err:=os.Remove(alias);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(input.Path,[]byte("changed-material"),0o600);err!=nil{t.Fatal(err)};if _,err:=readOwnedCredentialFile(input);err==nil{t.Fatal("material rotation reused old pin")}
}
func TestFCMOwnedFileConstructorUsesExplicitMaterialWithoutAmbientDiscovery(t *testing.T){
 raw:=syntheticOwnedCredential(t);input:=ownedCredentialFixtureFile(t,"credential.json",raw)
 t.Setenv("GOOGLE_APPLICATION_CREDENTIALS",filepath.Join(t.TempDir(),"does-not-exist"))
 transport,err:=NewOwnedHTTPV1TransportFromFile(context.Background(),input,frozenHash([]byte("frozen configuration seed")))
 if err!=nil || transport==nil || !frozenDigest(transport.Binding()){t.Fatal("explicit local material did not create offline transport",err)}
 if err:=transport.Close();err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 if transport,err:=NewOwnedHTTPV1TransportFromFile(ctx,input,frozenHash([]byte("seed")));err==nil || transport!=nil{t.Fatal("cancelled owner created a live credential pool")}
}

func ownedPolicyRegistryFixture(t *testing.T)(ownedFCMPolicyDocument,[]byte){
 t.Helper();raw:=syntheticOwnedCredential(t);credential:=ownedCredentialFixtureFile(t,"credential.json",raw)
 seed:=frozenHash([]byte("synthetic frozen configuration seed"))
 transport,err:=NewOwnedHTTPV1Transport(context.Background(),raw,credential.ExpectedSHA256,seed);if err!=nil{t.Fatal(err)}
 policy:=messagedelivery.KafkaDeliveryPolicy{Version:"ordinary-kafka-v1",Epoch:"87366d9c-9113-487c-8ce2-3508531b3284",ActivationSHA256:strings.Repeat("a",64),ConfigurationSHA256:strings.Repeat("b",64),Discovery:"zookeeper",Vendor:"fcm",VendorBinding:transport.Binding()}
 fingerprint,err:=policy.Fingerprint();if err!=nil{t.Fatal(err)};if err:=transport.Close();err!=nil{t.Fatal(err)}
 document:=ownedFCMPolicyDocument{Version:"ordinary-fcm-owned-policies-v1",Database:"synthetic_owned_registry",Epoch:policy.Epoch,ActivationSHA256:policy.ActivationSHA256,Entries:[]ownedFCMPolicyEntry{{Policy:policy,Fingerprint:fingerprint,BindingSeedSHA256:seed,CredentialPath:credential.Path,CredentialSHA256:credential.ExpectedSHA256}}}
 encoded,err:=json.Marshal(document);if err!=nil{t.Fatal(err)};return document,encoded
}
func TestFCMOwnedRegistryRetainsOriginalPolicyAndMaterialWithoutCurrentConfig(t *testing.T){
 document,raw:=ownedPolicyRegistryFixture(t)
 file:=ownedCredentialFixtureFile(t,"retained-policy-registry.json",raw)
 entries,err:=LoadOwnedKafkaFCMPolicies(context.Background(),file,document.Database,document.Epoch,document.ActivationSHA256)
 if err!=nil || len(entries)!=1{t.Fatal("valid retained adapter failed offline construction",err)}
 if entries[0].Policy!=document.Entries[0].Policy || entries[0].Fingerprint!=document.Entries[0].Fingerprint || entries[0].Transport.Binding()!=document.Entries[0].Policy.VendorBinding{t.Fatal("retained binding replaced by current material")}
 if err:=entries[0].Transport.Close();err!=nil{t.Fatal(err)}
 document.Entries[0].BindingSeedSHA256=strings.Repeat("c",64);changed,_:=json.Marshal(document)
 bad:=ownedCredentialFixtureFile(t,"changed-seed.json",changed)
 if entries,err:=LoadOwnedKafkaFCMPolicies(context.Background(),bad,document.Database,document.Epoch,document.ActivationSHA256);err==nil || entries!=nil{t.Fatal("changed seed was silently rebound to old policy")}
}
func TestFCMOwnedRegistryRejectsAmbiguousFieldsDuplicatesAndForeignBindings(t *testing.T){
 document,raw:=ownedPolicyRegistryFixture(t)
 if _,err:=decodeOwnedFCMPolicies(raw,document.Database,document.Epoch,document.ActivationSHA256);err!=nil{t.Fatal("valid parse rejected",err)}
 for _,test:=range []struct{name string;mutate func()[]byte}{
  {"database",func()[]byte{return bytes.Replace(raw,[]byte(document.Database),[]byte("foreign_database"),1)}},
  {"case-alias",func()[]byte{return bytes.Replace(raw,[]byte(`"vendorBinding"`),[]byte(`"VendorBinding"`),1)}},
  {"duplicate-nested",func()[]byte{return bytes.Replace(raw,[]byte(`"vendor":"fcm"`),[]byte(`"vendor":"fcm","Vendor":"fcm"`),1)}},
  {"duplicate-policy",func()[]byte{copy:=document;copy.Entries=append(append([]ownedFCMPolicyEntry(nil),document.Entries...),document.Entries[0]);v,_:=json.Marshal(copy);return v}},
  {"foreign-policy",func()[]byte{copy:=document;copy.Entries=append([]ownedFCMPolicyEntry(nil),document.Entries...);copy.Entries[0].Policy.Epoch="066d8181-907f-4cc5-ae60-c0a349c0bc58";v,_:=json.Marshal(copy);return v}},
 }{t.Run(test.name,func(t *testing.T){if _,err:=decodeOwnedFCMPolicies(test.mutate(),document.Database,document.Epoch,document.ActivationSHA256);err==nil{t.Fatal("ambiguous or foreign retained registry accepted")}})}
}
