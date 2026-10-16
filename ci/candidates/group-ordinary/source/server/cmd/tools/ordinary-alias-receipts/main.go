package main

import (
 "bytes"
 "context"
 "encoding/json"
 "flag"
 "io"
 "os"
 "os/signal"
 "syscall"
 "time"

 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type evidence struct {
 Version string `json:"version"`
 Decision string `json:"decision"`
 BranchID string `json:"branchID"`
 RecipientSHA256 string `json:"recipientSHA256"`
 Vendor string `json:"vendor"`
 Protocol string `json:"protocol"`
 RequestID string `json:"requestID"`
 BodySHA256 string `json:"bodySHA256"`
 AttemptID string `json:"attemptID"`
 RequestSHA256 string `json:"requestSHA256"`
 ProviderReceiptSHA256 string `json:"providerReceiptSHA256"`
 IndependentSourceSHA256 string `json:"independentSourceSHA256"`
}
// Wire receipt from the managed shutdown API. This is data, not authority from
// a public caller. Dedicated filesystem/operator access is an execution boundary.
type drainReceipt struct {
 Version string `json:"version"`
 InstanceID string `json:"instanceID"`
 WorkerJoined bool `json:"workerJoined"`
 OwnedPoolsClosed bool `json:"ownedPoolsClosed"`
 Policies []string `json:"policies"`
}
type commandResult struct {value any;code int}
func result(state string,code int)commandResult{return commandResult{map[string]string{"state":state},code}}
func main(){
 ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM)
 code:=run(ctx,os.Args[1:],os.Stdout);cancel();os.Exit(code)
}
func run(ctx context.Context,args []string,output io.Writer)int{
 outcome:=execute(ctx,args,openMongoBackend)
 // Print once, AFTER all cleanup results have been incorporated. A successful
 // CAS is never called rolled back if closing its local resources fails.
 if json.NewEncoder(output).Encode(outcome.value)!=nil{return 4};return outcome.code
}

// Exact case-sensitive root keys; the accepted formats contain no nested
// object. A duplicate, wrong spelling, unknown key or trailing input is invalid.
func decodeDoc(raw []byte,allowed map[string]bool,value any)error{
 d:=json.NewDecoder(bytes.NewReader(raw));token,err:=d.Token();if err!=nil || token!=json.Delim('{'){return errInput}
 seen:=map[string]bool{}
 for d.More(){token,err:=d.Token();key,ok:=token.(string);if err!=nil || !ok || !allowed[key] || seen[key]{return errInput};seen[key]=true;var discard json.RawMessage;if d.Decode(&discard)!=nil{return errInput}}
 if token,err=d.Token();err!=nil || token!=json.Delim('}') || d.Decode(new(any))!=io.EOF{return errInput}
 decoder:=json.NewDecoder(bytes.NewReader(raw));decoder.DisallowUnknownFields();if decoder.Decode(value)!=nil{return errInput};return nil
}
func confirmation(expected messagedelivery.KafkaAliasVendorSummary,proof evidence,drain drainReceipt,decision,evidenceSHA,drainSHA string)(messagedelivery.KafkaAliasVendorConfirmation,error){
 if proof.Version!="ordinary-alias-evidence-v1" || proof.Decision!=decision || proof.BranchID!=expected.BranchID || proof.RecipientSHA256!=expected.RecipientSHA256 || proof.Vendor!=expected.Vendor || proof.Protocol!=expected.Protocol || proof.RequestID!=expected.RequestID || proof.BodySHA256!=expected.BodySHA256 || proof.AttemptID!=expected.AttemptID || proof.RequestSHA256!=expected.RequestSHA256 || !operatorSHA(proof.IndependentSourceSHA256) || !operatorSHA(evidenceSHA) || !operatorSHA(drainSHA) || drain.Version!="ordinary-offline-drained-v1" || drain.InstanceID!=expected.InstanceID || !drain.WorkerJoined || !drain.OwnedPoolsClosed{return messagedelivery.KafkaAliasVendorConfirmation{},errInput}
 bound:=false;previous:=""
 for _,policy:=range drain.Policies{if !operatorSHA(policy) || policy<=previous{return messagedelivery.KafkaAliasVendorConfirmation{},errInput};previous=policy;if policy==expected.PolicySHA256{bound=true}}
 if !bound{return messagedelivery.KafkaAliasVendorConfirmation{},errInput}
 switch decision{case "provider-accepted","provider-ignored":if !operatorSHA(proof.ProviderReceiptSHA256){return messagedelivery.KafkaAliasVendorConfirmation{},errInput};case "not-applied","rejected":if proof.ProviderReceiptSHA256!=""{return messagedelivery.KafkaAliasVendorConfirmation{},errInput};default:return messagedelivery.KafkaAliasVendorConfirmation{},errInput}
 return messagedelivery.KafkaAliasVendorConfirmation{Decision:decision,EvidenceSHA256:evidenceSHA,ProviderReceiptSHA256:proof.ProviderReceiptSHA256,Drain:messagedelivery.KafkaAttemptDrainProof{InstanceID:drain.InstanceID,EvidenceSHA256:drainSHA}},nil
}

func execute(parent context.Context,args []string,open aliasBackendFactory)(outcome commandResult){
 flags:=flag.NewFlagSet("ordinary-alias-receipts",flag.ContinueOnError);flags.SetOutput(io.Discard)
 inputPath:=flags.String("input","","dedicated private Mongo operator input")
 inputSHA:=flags.String("input-sha","","exact private input pin; never printed")
 expectedDB:=flags.String("expected-database","","explicit database binding")
 branch:=flags.String("branch","","original branch UUID")
 user:=flags.String("recipient","","original recipient; output is hash only")

 decision:=flags.String("confirm","","empty means read-only inspect")
 expectedPath:=flags.String("expected-receipt","","exact saved original inspect summary")
 expectedSHA:=flags.String("expected-receipt-sha","","saved summary SHA")
 evidencePath:=flags.String("evidence","","independent endpoint evidence receipt")
 evidenceSHA:=flags.String("evidence-sha","","independent receipt SHA")
 drainPath:=flags.String("drain","","same original worker joined/pools closed receipt")
 drainSHA:=flags.String("drain-sha","","drain receipt SHA")
 journalPath:=flags.String("journal","","fresh private confirmation journal; required for mutation")
 if parent==nil || open==nil || flags.Parse(args)!=nil || flags.NArg()!=0 || !canonicalUUID(*branch) || *user==""{return result("invalid_input",2)}
 if parent.Err()!=nil{return result("cancelled_before_operator_input",4)}
 input,err:=loadInput(*inputPath,*expectedDB,*inputSHA);if err!=nil{return result("invalid_operator_input",2)}
 var expected messagedelivery.KafkaAliasVendorSummary;var proofValue messagedelivery.KafkaAliasVendorConfirmation
 var journal *privateJournal
 knownCommit:=false
 if *decision!=""{
  var proof evidence;var drained drainReceipt
  raw,err:=readPrivateDoc(*expectedPath,*expectedSHA)
  if err!=nil || decodeDoc(raw,map[string]bool{"BranchID":true,"RecipientSHA256":true,"PolicySHA256":true,"Vendor":true,"Protocol":true,"Binding":true,"RequestID":true,"BodySHA256":true,"PayloadSHA256":true,"RequestSHA256":true,"State":true,"OutcomeSHA256":true,"ProviderReceiptSHA256":true,"AttemptID":true,"InstanceID":true,"RecordSHA256":true,"Revision":true},&expected)!=nil{return result("invalid_expected_receipt",2)}
  raw,err=readPrivateDoc(*evidencePath,*evidenceSHA)
  if err!=nil || decodeDoc(raw,map[string]bool{"version":true,"decision":true,"branchID":true,"recipientSHA256":true,"vendor":true,"protocol":true,"requestID":true,"bodySHA256":true,"attemptID":true,"requestSHA256":true,"providerReceiptSHA256":true,"independentSourceSHA256":true},&proof)!=nil{return result("invalid_independent_evidence",2)}
  raw,err=readPrivateDoc(*drainPath,*drainSHA)
  if err!=nil || decodeDoc(raw,map[string]bool{"version":true,"instanceID":true,"workerJoined":true,"ownedPoolsClosed":true,"policies":true},&drained)!=nil{return result("invalid_drain_receipt",2)}
  if expected.BranchID!=*branch || expected.RecipientSHA256!=operatorHash([]byte(*user)){return result("expected_target_mismatch",2)}
  proofValue,err=confirmation(expected,proof,drained,*decision,*evidenceSHA,*drainSHA);if err!=nil{return result("confirmation_contract_rejected",2)}
  journal,err=newPrivateJournal(*journalPath);if err!=nil{return result("journal_creation_rejected",2)}
  defer func(){if journal.Close()!=nil{if knownCommit{outcome=result("confirmation_committed_local_cleanup_unconfirmed",4)}else{outcome=result("local_cleanup_unconfirmed_inspect_same_alias",4)}}}()
  if journal.Write(map[string]string{"state":"prepared_not_dispatched","expectedReceiptSHA256":*expectedSHA,"evidenceSHA256":*evidenceSHA,"drainSHA256":*drainSHA})!=nil{return result("journal_unconfirmed",4)}
 }else if *expectedPath!="" || *expectedSHA!="" || *evidencePath!="" || *evidenceSHA!="" || *drainPath!="" || *drainSHA!="" || *journalPath!=""{return result("inspect_cannot_consume_mutation_inputs",2)}
 ctx,cancel:=context.WithTimeout(parent,20*time.Second);defer cancel()
 backend,err:=open(ctx,input);if err!=nil || backend==nil{return result("operator_connection_or_catalog_unconfirmed",4)}
 defer func(){end,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel();if backend.Close(end)!=nil{if knownCommit{outcome=result("confirmation_committed_disconnect_unconfirmed",4)}else{outcome=result("operator_disconnect_unconfirmed",4)}}}()
 actual,err:=backend.Inspect(ctx,*branch,*user);if err!=nil{return result("original_alias_receipt_unconfirmed",4)}
 if *decision==""{return commandResult{actual,0}}
 a,_:=json.Marshal(actual);b,_:=json.Marshal(expected)
 if !bytes.Equal(a,b){return result("expected_receipt_changed",2)}
 if journal.Write(map[string]string{"state":"confirmation_attempt_starting","recordSHA256":actual.RecordSHA256,"decision":*decision})!=nil{return result("journal_unconfirmed",4)}
 if err:=backend.Confirm(ctx,*user,expected,proofValue);err!=nil{
  _ = journal.Write(map[string]string{"state":"unconfirmed_inspect_same_alias"})
  return result("unconfirmed_inspect_same_alias_no_replay",4)
 }
 knownCommit=true
 if journal.Write(map[string]string{"state":"confirmation_committed","originalRecordSHA256":actual.RecordSHA256})!=nil{return result("confirmation_committed_journal_unconfirmed_inspect",4)}
 return result("confirmation_committed_no_vendor_call",0)
}
