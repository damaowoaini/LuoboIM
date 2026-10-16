package aliasvendor

import (
 "bytes"
 "context"
 "encoding/json"
 "io"
 "path/filepath"

 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type OwnedOfflinePolicySpec struct {
 Policy messagedelivery.KafkaDeliveryPolicy `json:"policy"`
 Fingerprint string `json:"fingerprint"`
 BindingSeedSHA256 string `json:"bindingSeedSHA256"`
 CredentialPath string `json:"credentialPath"`
 CredentialSHA256 string `json:"credentialSHA256"`
}
type OwnedOfflinePolicyRegistry struct {
 Version string `json:"version"`
 Database string `json:"database"`
 Epoch string `json:"epoch"`
 ActivationSHA256 string `json:"activationSHA256"`
 CurrentPolicySHA256 string `json:"currentPolicySHA256"`
 Entries []OwnedOfflinePolicySpec `json:"entries"`
}
func registryKeys(raw []byte,keys ...string)bool{root,err:=object(raw);return err==nil && exact(root,keys...)}

// Concrete ordinary-offline startup input, not an HTTP/RPC payload or an auth
// capability. Only names/digests/private paths live in the registry. Each actual
// vendor constructor independently opens/verifies its exact private material.
func LoadOwnedOfflineRegistry(ctx context.Context,input OwnedInputFile,database,epoch,activation string)(OwnedOfflinePolicyRegistry,error){
 if ctx==nil || ctx.Err()!=nil || database==""{return OwnedOfflinePolicyRegistry{},ErrInput}
 raw,err:=readOwnedInputFile(input);if err!=nil{return OwnedOfflinePolicyRegistry{},ErrInput}
 defer func(){for i:=range raw{raw[i]=0}}()
 return decodeOwnedOfflineRegistry(raw,database,epoch,activation)
}
func decodeOwnedOfflineRegistry(raw []byte,database,epoch,activation string)(OwnedOfflinePolicyRegistry,error){
 var registry OwnedOfflinePolicyRegistry
 if !registryKeys(raw,"version","database","epoch","activationSHA256","currentPolicySHA256","entries"){return registry,ErrInput}
 d:=json.NewDecoder(bytes.NewReader(raw));d.DisallowUnknownFields()
 if d.Decode(&registry)!=nil || d.Decode(new(any))!=io.EOF || registry.Version!="ordinary-owned-offline-policies-v1" || registry.Database!=database || registry.Epoch!=epoch || registry.ActivationSHA256!=activation || !validDigest(registry.CurrentPolicySHA256) || len(registry.Entries)==0{return registry,ErrInput}
 root,_:=object(raw);var entries []json.RawMessage;if json.Unmarshal(root["entries"],&entries)!=nil || len(entries)!=len(registry.Entries){return registry,ErrInput}
 seen:=map[string]bool{};currentFound:=false
 for i,entry:=range registry.Entries{
  if !registryKeys(entries[i],"policy","fingerprint","bindingSeedSHA256","credentialPath","credentialSHA256"){return registry,ErrInput}
  fields,_:=object(entries[i])
  if !registryKeys(fields["policy"],"version","epoch","activationSHA256","configurationSHA256","discovery","beforeGroupPushBinding","beforeGroupFailureMode","onlinePushBinding","onlineFailureMode","offlinePushBinding","offlineFailureMode","vendor","vendorBinding","encryptionRequired"){return registry,ErrInput}
  actual,err:=entry.Policy.Fingerprint()
  if err!=nil || actual!=entry.Fingerprint || !validDigest(actual) || seen[actual] || !validDigest(entry.BindingSeedSHA256) || entry.Policy.Epoch!=epoch || entry.Policy.ActivationSHA256!=activation{return registry,ErrInput}
  seen[actual]=true;currentFound=currentFound || actual==registry.CurrentPolicySHA256
  if entry.Policy.Vendor=="none"{if entry.CredentialPath!="" || entry.CredentialSHA256!=""{return registry,ErrInput}}else if !filepath.IsAbs(entry.CredentialPath) || filepath.Clean(entry.CredentialPath)!=entry.CredentialPath || !validDigest(entry.CredentialSHA256){return registry,ErrInput}
 }
 if !currentFound{return registry,ErrInput};return registry,nil
}
