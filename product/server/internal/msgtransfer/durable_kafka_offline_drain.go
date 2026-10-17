package msgtransfer

import (
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "syscall"
)

// This is a shutdown action, not HTTP/RPC input. It writes only AFTER the SAME
// worker has joined and all its owned pools closed. Unknown business outcomes
// remain unknown. A failed/partial file stays in place and is never resumed.
func (r *OwnedFCMOfflineRuntime) StopAndWriteDrain(ctx context.Context,path string)(string,error){
 if err:=r.Stop(ctx);err!=nil{return "",err}
 summary,err:=r.DrainSummary();if err!=nil{return "",err}
 return writeKafkaOfflineDrainSummary(path,summary)
}
func writeKafkaOfflineDrainSummary(path string,summary KafkaOfflineDrainSummary)(digest string,err error){
 if !filepath.IsAbs(path) || filepath.Clean(path)!=path || summary.Version!="ordinary-offline-drained-v1" || summary.InstanceID=="" || !summary.WorkerJoined || !summary.OwnedPoolsClosed || len(summary.Policies)==0{return "",ErrKafkaOfflineShutdownPending}
 parentPath:=filepath.Dir(path);canonical,e:=filepath.EvalSymlinks(parentPath)
 if e!=nil || canonical!=parentPath{return "",ErrKafkaOfflineShutdownPending}
 privateParent:=func(info os.FileInfo)bool{if info==nil || !info.IsDir() || info.Mode().Perm()!=0o700{return false};owner,ok:=info.Sys().(*syscall.Stat_t);return ok && owner.Uid==uint32(os.Geteuid())}
 parent,e:=os.Lstat(parentPath);if e!=nil || !privateParent(parent){return "",ErrKafkaOfflineShutdownPending}
 directory,e:=os.OpenFile(parentPath,os.O_RDONLY|syscall.O_NOFOLLOW,0);if e!=nil{return "",ErrKafkaOfflineShutdownPending}
 defer func(){if directory.Close()!=nil{digest="";err=ErrKafkaOfflineShutdownPending}}()
 opened,e:=directory.Stat();if e!=nil || !os.SameFile(parent,opened){return "",ErrKafkaOfflineShutdownPending}
 root,e:=os.OpenRoot(parentPath);if e!=nil{return "",ErrKafkaOfflineShutdownPending}
 defer func(){if root.Close()!=nil{digest="";err=ErrKafkaOfflineShutdownPending}}()
 rooted,e:=root.Stat(".");if e!=nil || !os.SameFile(parent,rooted){return "",ErrKafkaOfflineShutdownPending}
 name:=filepath.Base(path);file,e:=root.OpenFile(name,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0o600);if e!=nil{return "",ErrKafkaOfflineShutdownPending}
 defer func(){if file.Close()!=nil{digest="";err=ErrKafkaOfflineShutdownPending}}()
 identity,e:=file.Stat();if e!=nil{return "",ErrKafkaOfflineShutdownPending}
 current:=func()bool{
  now,e:=os.Lstat(parentPath);if e!=nil || !privateParent(now) || !os.SameFile(parent,now){return false}
  child,e:=root.Lstat(name);if e!=nil || !os.SameFile(identity,child) || !child.Mode().IsRegular() || child.Mode().Perm()!=0o600{return false}
  atPath,e:=os.Lstat(path);if e!=nil || !os.SameFile(identity,atPath){return false}
  st,ok:=child.Sys().(*syscall.Stat_t);return ok && st.Uid==uint32(os.Geteuid()) && st.Nlink==1
 }
 if !current() || json.NewEncoder(file).Encode(summary)!=nil || file.Sync()!=nil || directory.Sync()!=nil || !current(){return "",ErrKafkaOfflineShutdownPending}
 return summary.SHA256(),nil
}
