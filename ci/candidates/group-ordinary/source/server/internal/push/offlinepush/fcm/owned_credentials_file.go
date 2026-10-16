package fcm

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

var ErrOwnedCredentialFile=errors.New("owned FCM credential input invalid or changed")

type OwnedCredentialFile struct { Path,ExpectedSHA256 string }

// The only production credential loader: explicit source-owned file, never
// ADC, runtime YAML credential fallback, arbitrary URL, or caller document.
// Parent must be an owned private0700 directory; file must be owned0600,
// canonical/no-symlink/regular/single-link and exactly match the deployment pin.
// Fixed errors only: no file contents or underlying credential errors escape.
func readOwnedCredentialFile(input OwnedCredentialFile)([]byte,error){
	if !filepath.IsAbs(input.Path) || filepath.Clean(input.Path)!=input.Path || !frozenDigest(input.ExpectedSHA256){return nil,ErrOwnedCredentialFile}
	resolved,err:=filepath.EvalSymlinks(input.Path);if err!=nil || resolved!=input.Path{return nil,ErrOwnedCredentialFile}
	parentPath:=filepath.Dir(input.Path)
	parent,err:=os.Lstat(parentPath);if err!=nil || !privateOwned(parent,true){return nil,ErrOwnedCredentialFile}
	before,err:=os.Lstat(input.Path);if err!=nil || !privateOwned(before,false) || before.Size()<1 || before.Size()>128<<10{return nil,ErrOwnedCredentialFile}
	file,err:=os.OpenFile(input.Path,os.O_RDONLY|syscall.O_NOFOLLOW,0);if err!=nil{return nil,ErrOwnedCredentialFile}
	opened,statErr:=file.Stat()
	if statErr!=nil || !privateOwned(opened,false) || !sameOwnedFile(before,opened){file.Close();return nil,ErrOwnedCredentialFile}
	body,readErr:=io.ReadAll(io.LimitReader(file,(128<<10)+1))
	after,statErr:=file.Stat();closeErr:=file.Close()
	current,pathErr:=os.Lstat(input.Path);currentParent,parentErr:=os.Lstat(parentPath)
	if readErr!=nil || statErr!=nil || closeErr!=nil || pathErr!=nil || parentErr!=nil || !sameOwnedFile(opened,after) || !sameOwnedFile(opened,current) || !sameOwnedFile(parent,currentParent) || !privateOwned(current,false) || !privateOwned(currentParent,true) || len(body)!=int(before.Size()) || frozenHash(body)!=input.ExpectedSHA256{
		for i:=range body{body[i]=0};return nil,ErrOwnedCredentialFile
	}
	return body,nil
}

func privateOwned(info os.FileInfo,directory bool)bool{
	if info==nil{return false}
	st,ok:=info.Sys().(*syscall.Stat_t);if !ok || st.Uid!=uint32(os.Geteuid()){return false}
	if directory{return info.IsDir() && info.Mode().Perm()==0o700 && info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)==0}
	return info.Mode().IsRegular() && info.Mode().Perm()==0o600 && info.Mode()&(os.ModeSetuid|os.ModeSetgid)==0 && st.Nlink==1
}
func sameOwnedFile(left,right os.FileInfo)bool{
	return left!=nil && right!=nil && os.SameFile(left,right) && left.Mode()==right.Mode() && left.Size()==right.Size() && left.ModTime().Equal(right.ModTime())
}

func NewOwnedHTTPV1TransportFromFile(ctx context.Context,input OwnedCredentialFile,configurationSHA string)(*HTTPV1Transport,error){
	if ctx==nil || ctx.Err()!=nil{return nil,ErrOwnedCredentialFile}
	body,err:=readOwnedCredentialFile(input);if err!=nil{return nil,err}
	defer func(){for i:=range body{body[i]=0}}()
	transport,err:=NewOwnedHTTPV1Transport(ctx,body,input.ExpectedSHA256,configurationSHA)
	if err!=nil{return nil,ErrOwnedCredentialFile};return transport,nil
}
