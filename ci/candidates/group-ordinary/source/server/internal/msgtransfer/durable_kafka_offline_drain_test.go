package msgtransfer

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestOwnedOfflineDrainFileDurablyBindsSummaryAndNeverClobbersExistingReceipt(t *testing.T){
 directory:=t.TempDir();if err:=os.Chmod(directory,0o700);err!=nil{t.Fatal(err)}
 summary:=KafkaOfflineDrainSummary{Version:"ordinary-offline-drained-v1",InstanceID:"c66e061e-0f49-4e3f-961c-546db9fe2a55",WorkerJoined:true,OwnedPoolsClosed:true,Policies:[]string{strings.Repeat("a",64)}}
 path:=filepath.Join(directory,"original-instance-drain.json")
 digest,err:=writeKafkaOfflineDrainSummary(path,summary);if err!=nil || digest!=summary.SHA256(){t.Fatal("known same-instance drain was not durably recorded",err)}
 body,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 info,err:=os.Lstat(path);if err!=nil || info.Mode().Perm()!=0o600{t.Fatal("drain output is not private",err)}
 if _,err:=writeKafkaOfflineDrainSummary(path,summary);err==nil{t.Fatal("existing drain proof overwritten")}
 after,err:=os.ReadFile(path);if err!=nil || string(after)!=string(body){t.Fatal("existing drain proof changed",err)}
 summary.WorkerJoined=false
 if _,err:=writeKafkaOfflineDrainSummary(filepath.Join(directory,"active.json"),summary);err==nil{t.Fatal("active worker acquired filesystem drain proof")}
 if _,err:=os.Lstat(filepath.Join(directory,"active.json"));!os.IsNotExist(err){t.Fatal("invalid proof created an output")}
}
