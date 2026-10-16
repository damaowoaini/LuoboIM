package main

import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type deviceBackendFixture struct {summary messagedelivery.KafkaFCMDeviceSummary;inspectCalls,confirmCalls,closeCalls int;confirmErr,closeErr error;onConfirm func()}
func (b *deviceBackendFixture) Inspect(ctx context.Context,branch,user,device string)(messagedelivery.KafkaFCMDeviceSummary,error){
 b.inspectCalls++;deadline,ok:=ctx.Deadline();if !ok || time.Until(deadline)>20*time.Second{return messagedelivery.KafkaFCMDeviceSummary{},errInput}
 if branch!=b.summary.BranchID || operatorHash([]byte(user))!=b.summary.RecipientSHA256 || device!=b.summary.DeviceSHA256{return messagedelivery.KafkaFCMDeviceSummary{},errInput};return b.summary,nil
}
func (b *deviceBackendFixture) Confirm(context.Context,string,messagedelivery.KafkaFCMDeviceSummary,messagedelivery.KafkaFCMDeviceConfirmation)error{b.confirmCalls++;if b.onConfirm!=nil{b.onConfirm()};return b.confirmErr}
func (b *deviceBackendFixture) Close(ctx context.Context)error{b.closeCalls++;deadline,ok:=ctx.Deadline();if !ok || time.Until(deadline)>3*time.Second{return errInput};return b.closeErr}
func operatorFixtureDoc(t *testing.T,dir,name string,value any)(string,string){t.Helper();raw,err:=json.Marshal(value);if err!=nil{t.Fatal(err)};raw=append(raw,'\n');path:=filepath.Join(dir,name);if err:=os.WriteFile(path,raw,0o600);err!=nil{t.Fatal(err)};return path,operatorHash(raw)}
func operatorFixture(t *testing.T)([]string,*deviceBackendFixture,string){
 t.Helper();dir:=t.TempDir();if err:=os.Chmod(dir,0o700);err!=nil{t.Fatal(err)}
 input:=operatorInput{Version:1,Purpose:purpose,URI:"mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration",Database:"synthetic_operator_test",ReplicaSet:"luoboIntegration",Epoch:"41b33802-7959-4296-82e1-8d817bd8bf34",Activation:strings.Repeat("a",64)}
 path,pin:=operatorFixtureDoc(t,dir,"operator-input.json",input)
 summary:=messagedelivery.KafkaFCMDeviceSummary{BranchID:"a42b00a9-d9f4-4d02-a80f-81e604180bf5",RecipientSHA256:operatorHash([]byte("synthetic-recipient")),DeviceSHA256:strings.Repeat("b",64),PolicySHA256:strings.Repeat("c",64),ManifestSHA256:strings.Repeat("d",64),RequestSHA256:strings.Repeat("e",64),State:"unconfirmed",AttemptID:"cadf797c-28c0-45a9-907c-f01353dc8f4c",InstanceID:"bcd54f59-707d-49fb-a2dc-7c38b6ef7ab0",RecordSHA256:strings.Repeat("f",64),Revision:5}
 args:=[]string{"--input",path,"--input-sha",pin,"--expected-database",input.Database,"--branch",summary.BranchID,"--recipient","synthetic-recipient","--device-sha",summary.DeviceSHA256}
 return args,&deviceBackendFixture{summary:summary},dir
}
func operatorConfirmationFixture(t *testing.T,args []string,b *deviceBackendFixture,dir string)([]string,string){
 t.Helper();expected,expectedSHA:=operatorFixtureDoc(t,dir,"expected.json",b.summary)
 proof:=evidence{Version:"ordinary-device-evidence-v1",Decision:"applied",BranchID:b.summary.BranchID,RecipientSHA256:b.summary.RecipientSHA256,DeviceSHA256:b.summary.DeviceSHA256,AttemptID:b.summary.AttemptID,RequestSHA256:b.summary.RequestSHA256,ProviderReceiptSHA256:strings.Repeat("1",64),IndependentSourceSHA256:strings.Repeat("2",64)}
 evidencePath,evidenceSHA:=operatorFixtureDoc(t,dir,"evidence.json",proof)
 drained:=drainReceipt{Version:"ordinary-offline-drained-v1",InstanceID:b.summary.InstanceID,WorkerJoined:true,OwnedPoolsClosed:true,Policies:[]string{b.summary.PolicySHA256}}
 drainPath,drainSHA:=operatorFixtureDoc(t,dir,"drain.json",drained);journal:=filepath.Join(dir,"confirmation.jsonl")
 return append(append([]string(nil),args...),"--confirm","applied","--expected-receipt",expected,"--expected-receipt-sha",expectedSHA,"--evidence",evidencePath,"--evidence-sha",evidenceSHA,"--drain",drainPath,"--drain-sha",drainSHA,"--journal",journal),journal
}
func fixtureFactory(b *deviceBackendFixture)deviceBackendFactory{return func(context.Context,operatorInput)(deviceBackend,error){return b,nil}}
func resultState(r commandResult)string{value,_:=r.value.(map[string]string);return value["state"]}

func TestOrdinaryDeviceOperatorDefaultInspectHasNoMutationAndChecksDisconnect(t *testing.T){
 args,b,_:=operatorFixture(t)
 actual:=execute(context.Background(),args,fixtureFactory(b))
 if actual.code!=0 || b.inspectCalls!=1 || b.confirmCalls!=0 || b.closeCalls!=1{t.Fatal("default was not exactly read-only inspect")}
 if summary,ok:=actual.value.(messagedelivery.KafkaFCMDeviceSummary);!ok || summary!=b.summary{t.Fatal("minimal inspect summary changed")}
 b.closeErr=errors.New("synthetic closed result unknown")
 actual=execute(context.Background(),args,fixtureFactory(b));if actual.code!=4 || resultState(actual)!="operator_disconnect_unconfirmed"{t.Fatal("unknown disconnect became successful inspect")}
 raw,err:=json.Marshal(actual.value);if err!=nil || bytes.Contains(raw,[]byte("mongodb://")) || bytes.Contains(raw,[]byte("synthetic-recipient")){t.Fatal("operator output leaked input")}
}
func TestOrdinaryDeviceOperatorUnknownConfirmIsJournaledOnceAndNeverAutoReplayed(t *testing.T){
 args,b,dir:=operatorFixture(t);args,journal:=operatorConfirmationFixture(t,args,b,dir)
 b.confirmErr=messagedelivery.ErrKafkaOfflineUnconfirmed
 b.onConfirm=func(){raw,err:=os.ReadFile(journal);if err!=nil || !bytes.Contains(raw,[]byte("confirmation_attempt_starting")){t.Error("CAS dispatched before durable attempt intent")}}
 actual:=execute(context.Background(),args,fixtureFactory(b))
 if actual.code!=4 || b.confirmCalls!=1 || b.closeCalls!=1 || resultState(actual)!="unconfirmed_inspect_same_device_no_replay"{t.Fatal("unknown confirmation acquired an ACK")}
 raw,err:=os.ReadFile(journal);if err!=nil || !bytes.Contains(raw,[]byte("unconfirmed_inspect_same_device")) || bytes.Contains(raw,[]byte("confirmation_committed")){t.Fatal("unknown journal invalid",err)}
 actual=execute(context.Background(),args,fixtureFactory(b));if actual.code!=2 || b.confirmCalls!=1 || b.inspectCalls!=1{t.Fatal("same journal caused automatic replay")}
}
func TestOrdinaryDeviceOperatorKnownCommitAndLocalCleanupRemainDistinct(t *testing.T){
 args,b,dir:=operatorFixture(t);args,journal:=operatorConfirmationFixture(t,args,b,dir)
 b.closeErr=errors.New("synthetic disconnect unknown")
 actual:=execute(context.Background(),args,fixtureFactory(b))
 if actual.code!=4 || resultState(actual)!="confirmation_committed_disconnect_unconfirmed" || b.confirmCalls!=1{t.Fatal("committed confirmation was called rolled back or clean",resultState(actual))}
 raw,err:=os.ReadFile(journal);if err!=nil || !bytes.Contains(raw,[]byte("confirmation_committed")){t.Fatal("known commit receipt missing",err)}
}
func TestOrdinaryDeviceOperatorStaleExpectedReceiptCannotConfirm(t *testing.T){
 args,b,dir:=operatorFixture(t);args,_=operatorConfirmationFixture(t,args,b,dir);b.summary.Revision++
 actual:=execute(context.Background(),args,fixtureFactory(b));if actual.code!=2 || resultState(actual)!="expected_receipt_changed" || b.confirmCalls!=0{t.Fatal("stale original summary mutated receipt")}
}
func TestOrdinaryDeviceOperatorForeignProofAndUndrainedInstanceRejectBeforeConnection(t *testing.T){
 args,b,_:=operatorFixture(t)
 expected:=b.summary
 valid:=evidence{Version:"ordinary-device-evidence-v1",Decision:"applied",BranchID:expected.BranchID,RecipientSHA256:expected.RecipientSHA256,DeviceSHA256:expected.DeviceSHA256,AttemptID:expected.AttemptID,RequestSHA256:expected.RequestSHA256,ProviderReceiptSHA256:strings.Repeat("1",64),IndependentSourceSHA256:strings.Repeat("2",64)}
 drained:=drainReceipt{Version:"ordinary-offline-drained-v1",InstanceID:expected.InstanceID,WorkerJoined:true,OwnedPoolsClosed:true,Policies:[]string{expected.PolicySHA256}}
 if _,err:=confirmation(expected,valid,drained,"applied",strings.Repeat("3",64),strings.Repeat("4",64));err!=nil{t.Fatal("valid independent proof rejected",err)}
 for _,fault:=range []string{"sibling","attempt","request","instance","active-worker","open-pools","different-policy","duplicate-policy"}{t.Run(fault,func(t *testing.T){
  proof,drain:=valid,drained;drain.Policies=append([]string(nil),drained.Policies...)
  switch fault{case "sibling":proof.DeviceSHA256=strings.Repeat("5",64);case "attempt":proof.AttemptID="another-attempt";case "request":proof.RequestSHA256=strings.Repeat("6",64);case "instance":drain.InstanceID="restarted-worker";case "active-worker":drain.WorkerJoined=false;case "open-pools":drain.OwnedPoolsClosed=false;case "different-policy":drain.Policies=[]string{strings.Repeat("7",64)};case "duplicate-policy":drain.Policies=append(drain.Policies,drain.Policies[0])}
  if _,err:=confirmation(expected,proof,drain,"applied",strings.Repeat("3",64),strings.Repeat("4",64));err==nil{t.Fatal("foreign or active authority accepted")}
 })}
 opened:=0
 actual:=execute(context.Background(),append(args,"--evidence-sha",strings.Repeat("3",64)),func(context.Context,operatorInput)(deviceBackend,error){opened++;return b,nil})
 if actual.code!=2 || opened!=0{t.Fatal("inspect silently consumed mutation evidence")}
}
func TestOrdinaryDeviceOperatorPrivateJournalCannotBeClobberedOrRetargeted(t *testing.T){
 directory:=t.TempDir();if err:=os.Chmod(directory,0o700);err!=nil{t.Fatal(err)};path:=filepath.Join(directory,"intent.jsonl")
 journal,err:=newPrivateJournal(path);if err!=nil{t.Fatal(err)}
 if err:=journal.Write(map[string]string{"state":"prepared"});err!=nil{t.Fatal(err)}
 if _,err:=newPrivateJournal(path);err==nil{t.Fatal("existing intent overwritten")}
 if err:=os.Rename(path,path+".original");err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(path,[]byte("replacement must survive"),0o600);err!=nil{t.Fatal(err)}
 if err:=journal.Write(map[string]string{"state":"dispatched"});err==nil{t.Fatal("replaced journal accepted another dispatch")}
 if err:=journal.Close();err==nil{t.Fatal("replaced journal closed successfully")}
 raw,err:=os.ReadFile(path);if err!=nil || string(raw)!="replacement must survive"{t.Fatal("foreign replacement mutated",err)}
}
func TestOrdinaryDeviceOperatorInputCannotBorrowAmbientOrWeakReplicaConfiguration(t *testing.T){
 input:=operatorInput{Version:1,Purpose:purpose,URI:"mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration",Database:"synthetic_operator_test",ReplicaSet:"luoboIntegration",Epoch:"41b33802-7959-4296-82e1-8d817bd8bf34",Activation:strings.Repeat("a",64)}
 raw,_:=json.Marshal(input);if _,err:=decodeInput(raw,input.Database);err!=nil{t.Fatal("valid explicit input rejected",err)}
 for _,uri:=range []string{"mongodb+srv://external.invalid/?replicaSet=luoboIntegration","mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&directConnection=true","mongodb://127.0.0.1:37017/foreign?replicaSet=luoboIntegration","mongodb://127.0.0.1:37017/?replicaSet=other","mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&readPreference=secondary","mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&authMechanism=MONGODB-AWS","mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&tlsInsecure=true"}{
  copy:=input;copy.URI=uri;raw,_:=json.Marshal(copy);if _,err:=decodeInput(raw,input.Database);err==nil{t.Fatal("weak or ambient-auth input accepted")}
 }
 duplicate:=append(bytes.TrimSuffix(raw,[]byte("}")),[]byte(`,"Database":"synthetic_operator_test"}`)...)
 if _,err:=decodeInput(duplicate,input.Database);err==nil{t.Fatal("case-folded operator field accepted")}
}
