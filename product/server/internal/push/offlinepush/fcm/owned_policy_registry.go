package fcm

import (
 "bytes"
 "context"
 "encoding/json"
 "io"

 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type ownedFCMPolicyEntry struct {
 Policy messagedelivery.KafkaDeliveryPolicy `json:"policy"`
 Fingerprint string `json:"fingerprint"`
 BindingSeedSHA256 string `json:"bindingSeedSHA256"`
 CredentialPath string `json:"credentialPath"`
 CredentialSHA256 string `json:"credentialSHA256"`
}
type ownedFCMPolicyDocument struct {
 Version string `json:"version"`
 Database string `json:"database"`
 Epoch string `json:"epoch"`
 ActivationSHA256 string `json:"activationSHA256"`
 Entries []ownedFCMPolicyEntry `json:"entries"`
}
type OwnedKafkaFCMPolicy struct {
 Policy messagedelivery.KafkaDeliveryPolicy
 Fingerprint string
 Transport *HTTPV1Transport
}

// The registry and each referenced credential are separate pinned0700/0600
// inputs owned by this runtime. Old policy recovery never substitutes current
// YAML, current credential bytes, ADC or the current policy for a missing one.
// Parsing/constructing these transports performs no provider HTTP request.
func LoadOwnedKafkaFCMPolicies(ctx context.Context,input OwnedCredentialFile,database,epoch,activation string)(result []OwnedKafkaFCMPolicy,err error){
 if ctx==nil || ctx.Err()!=nil || database==""{return nil,ErrOwnedCredentialFile}
 raw,err:=readOwnedCredentialFile(input);if err!=nil{return nil,ErrOwnedCredentialFile}
 defer func(){for i:=range raw{raw[i]=0}}()
 document,err:=decodeOwnedFCMPolicies(raw,database,epoch,activation);if err!=nil{return nil,err}
 complete:=false
 defer func(){if !complete{for _,entry:=range result{_ = entry.Transport.Close()};result=nil;err=ErrOwnedCredentialFile}}()
 for _,entry:=range document.Entries{
  transport,e:=NewOwnedHTTPV1TransportFromFile(ctx,OwnedCredentialFile{Path:entry.CredentialPath,ExpectedSHA256:entry.CredentialSHA256},entry.BindingSeedSHA256)
  if e!=nil{return result,ErrOwnedCredentialFile}
  if transport.Binding()!=entry.Policy.VendorBinding{_ = transport.Close();return result,ErrOwnedCredentialFile}
  result=append(result,OwnedKafkaFCMPolicy{Policy:entry.Policy,Fingerprint:entry.Fingerprint,Transport:transport})
 }
 complete=true;return result,nil
}

func exactRegistryKeys(raw []byte,keys ...string)bool{
 object,err:=httpV1Object(raw);if err!=nil || len(object)!=len(keys){return false}
 for _,key:=range keys{if _,ok:=object[key];!ok{return false}};return true
}
func decodeOwnedFCMPolicies(raw []byte,database,epoch,activation string)(ownedFCMPolicyDocument,error){
 var document ownedFCMPolicyDocument
 if !exactRegistryKeys(raw,"version","database","epoch","activationSHA256","entries"){return document,ErrOwnedCredentialFile}
 decoder:=json.NewDecoder(bytes.NewReader(raw));decoder.DisallowUnknownFields()
 if decoder.Decode(&document)!=nil || decoder.Decode(new(any))!=io.EOF || document.Version!="ordinary-fcm-owned-policies-v1" || document.Database!=database || document.Epoch!=epoch || document.ActivationSHA256!=activation || len(document.Entries)==0{return document,ErrOwnedCredentialFile}
 object,_:=httpV1Object(raw);var entries []json.RawMessage
 if json.Unmarshal(object["entries"],&entries)!=nil || len(entries)!=len(document.Entries){return document,ErrOwnedCredentialFile}
 seen:=map[string]bool{}
 for i,entry:=range document.Entries{
  if !exactRegistryKeys(entries[i],"policy","fingerprint","bindingSeedSHA256","credentialPath","credentialSHA256"){return document,ErrOwnedCredentialFile}
  fields,_:=httpV1Object(entries[i])
  if !exactRegistryKeys(fields["policy"],"version","epoch","activationSHA256","configurationSHA256","discovery","beforeGroupPushBinding","beforeGroupFailureMode","onlinePushBinding","onlineFailureMode","offlinePushBinding","offlineFailureMode","vendor","vendorBinding","encryptionRequired"){return document,ErrOwnedCredentialFile}
  fingerprint,err:=entry.Policy.Fingerprint()
  if err!=nil || !frozenDigest(entry.Fingerprint) || fingerprint!=entry.Fingerprint || seen[fingerprint] || entry.Policy.Epoch!=epoch || entry.Policy.ActivationSHA256!=activation || entry.Policy.Vendor!="fcm" || !frozenDigest(entry.BindingSeedSHA256) || !frozenDigest(entry.CredentialSHA256) || entry.CredentialPath==""{return document,ErrOwnedCredentialFile}
  seen[fingerprint]=true
 }
 return document,nil
}
