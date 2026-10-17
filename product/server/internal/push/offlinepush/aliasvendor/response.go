package aliasvendor

import (
 "bytes"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "io"
 "strconv"
 "strings"
)

var ErrInput=errors.New("owned alias vendor input rejected")
var ErrUnknown=errors.New("alias provider outcome unknown; inspect exact original receipt")
func digest(raw []byte)string{sum:=sha256.Sum256(raw);return hex.EncodeToString(sum[:])}
func validDigest(value string)bool{raw,err:=hex.DecodeString(value);return err==nil && len(raw)==32 && hex.EncodeToString(raw)==value}
func strictValue(d *json.Decoder,depth int)error{
 if depth>64{return ErrUnknown};token,err:=d.Token();if err!=nil{return ErrUnknown}
 delimiter,container:=token.(json.Delim);if !container{return nil}
 switch delimiter{
 case '{':
  seen:=map[string]bool{}
  for d.More(){key,err:=d.Token();name,ok:=key.(string);if err!=nil || !ok || seen[strings.ToLower(name)]{return ErrUnknown};seen[strings.ToLower(name)]=true;if strictValue(d,depth+1)!=nil{return ErrUnknown}}
  end,err:=d.Token();if err!=nil || end!=json.Delim('}'){return ErrUnknown}
 case '[':
  for d.More(){if strictValue(d,depth+1)!=nil{return ErrUnknown}}
  end,err:=d.Token();if err!=nil || end!=json.Delim(']'){return ErrUnknown}
 default:return ErrUnknown
 };return nil
}
func object(raw []byte)(map[string]json.RawMessage,error){
 d:=json.NewDecoder(bytes.NewReader(raw));d.UseNumber();if strictValue(d,0)!=nil || d.Decode(new(any))!=io.EOF{return nil,ErrUnknown}
 var value map[string]json.RawMessage;if json.Unmarshal(raw,&value)!=nil || value==nil{return nil,ErrUnknown};return value,nil
}
func exact(value map[string]json.RawMessage,keys ...string)bool{if len(value)!=len(keys){return false};for _,key:=range keys{if _,ok:=value[key];!ok{return false}};return true}
func decimal(raw json.RawMessage)(string,bool){
 var value string
 if json.Unmarshal(raw,&value)!=nil{var number json.Number;if json.Unmarshal(raw,&number)!=nil{return "",false};value=number.String()}
 if value=="" || len(value)>32{return "",false};for i,r:=range value{if r<'0' || r>'9' || i==0 && r=='0' && len(value)!=1{return "",false}};return value,true
}
// A successful creation/acceptance response is NOT a device delivery receipt.
// Every ambiguous, partial, wrong-case, duplicate or unexpected result stays
// unknown. No response here authorizes token deletion or an automatic retry.
func providerOutcome(vendor string,status int,raw []byte,complete bool)(accepted,ignored bool){
 if status!=200 || !complete{return false,false}
 root,err:=object(raw);if err!=nil{return false,false}
 switch vendor{
 case "jpush":
  if !exact(root,"sendno","msg_id"){return false,false}
  sendno,ok:=decimal(root["sendno"]);if !ok || sendno!="0"{return false,false}
  message,ok:=decimal(root["msg_id"]);return ok && message!="0",false
 case "getui":
  if !exact(root,"code","msg","data"){return false,false}
  var code int;var message string
  if json.Unmarshal(root["code"],&code)!=nil || code!=0 || json.Unmarshal(root["msg"],&message)!=nil{return false,false}
  tasks,err:=object(root["data"]);if err!=nil || len(tasks)!=1{return false,false}
  total,active:=0,0
  for task,raw:=range tasks{
   if task=="" || len(task)>512 || strings.ContainsAny(task,"\x00\r\n"){return false,false}
   devices,err:=object(raw);if err!=nil || len(devices)==0{return false,false}
   for cid,raw:=range devices{
    if cid=="" || len(cid)>512 || strings.ContainsAny(cid,"\x00\r\n"){return false,false}
    var state string;if json.Unmarshal(raw,&state)!=nil{return false,false}
    switch state{case "successed_online","successed_offline":active++;case "successed_ignore":default:return false,false};total++
   }
  }
  return active>0,total>0 && active==0
 };return false,false
}
func getuiAuthResult(status int,raw []byte,nowMS int64)(string,int64,error){
 if status!=200{return "",0,ErrUnknown};root,err:=object(raw);if err!=nil || !exact(root,"code","msg","data"){return "",0,ErrUnknown}
 var code int;var message string;if json.Unmarshal(root["code"],&code)!=nil || code!=0 || json.Unmarshal(root["msg"],&message)!=nil{return "",0,ErrUnknown}
 data,err:=object(root["data"]);if err!=nil || !exact(data,"token","expire_time"){return "",0,ErrUnknown}
 var token,expire string;if json.Unmarshal(data["token"],&token)!=nil || token=="" || len(token)>16384 || strings.ContainsAny(token,"\x00\r\n") || json.Unmarshal(data["expire_time"],&expire)!=nil{return "",0,ErrUnknown}
 expiry,err:=strconv.ParseInt(expire,10,64);if err!=nil || expiry<=nowMS{return "",0,ErrUnknown};return token,expiry,nil
}
