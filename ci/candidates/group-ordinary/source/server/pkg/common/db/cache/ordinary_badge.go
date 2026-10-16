package cache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrOrdinaryBadgeUnprepared=errors.New("ordinary badge epoch unavailable or result unconfirmed")

const ordinaryBadgeOwner="ORDINARY_BADGE_OWNER:"
const ordinaryBadgeEpoch="ORDINARY_BADGE_EPOCH:"

type OrdinaryBadgeLedger struct {
	rdb redis.UniversalClient
	scope,epoch,control,activation string
}

// This non-secret receipt binds the dedicated migration/drain proof. Preparing
// it is a separate operator action after old badge writers are drained; a new
// worker never creates/changes this global readiness key to make itself ready.
func ordinaryBadgeControl(scope,epoch,activation,evidenceSHA string)(string,error){
	if !badgeSHA(scope) || !badgeUUID(epoch) || !badgeSHA(activation) || !badgeSHA(evidenceSHA){return "",ErrOrdinaryBadgeUnprepared}
	body,err:=json.Marshal([]string{"ordinary-badge-v1",scope,epoch,activation,evidenceSHA})
	if err!=nil{return "",ErrOrdinaryBadgeUnprepared};return badgeHash(body),nil
}

func NewOrdinaryBadgeLedger(rdb redis.UniversalClient,scope,epoch,activation,evidenceSHA string)(*OrdinaryBadgeLedger,error){
	control,err:=ordinaryBadgeControl(scope,epoch,activation,evidenceSHA)
	if err!=nil || rdb==nil{return nil,ErrOrdinaryBadgeUnprepared}
	return &OrdinaryBadgeLedger{rdb:rdb,scope:scope,epoch:epoch,control:control,activation:activation},nil
}

func (l *OrdinaryBadgeLedger) BoundTo(scope,epoch,activation string)bool{return l!=nil && l.rdb!=nil && l.scope==scope && l.epoch==epoch && l.activation==activation}

// Explicit operator preparation is conditional and never overwrites an old or
// unknown epoch. A lost ACK is resolved by this same exact key/value; it does
// not authorize a second epoch or delete old receipts. No runtime calls this.
func PrepareOrdinaryBadgeEpoch(ctx context.Context,rdb redis.UniversalClient,scope,epoch,activation,evidenceSHA string)error{
	control,err:=ordinaryBadgeControl(scope,epoch,activation,evidenceSHA)
	if err!=nil || ctx==nil || rdb==nil{return ErrOrdinaryBadgeUnprepared}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	result,err:=rdb.Eval(current,`local t=redis.call('TYPE',KEYS[1]).ok
if t=='none' then redis.call('SET',KEYS[1],ARGV[1]);return 1 end
if t~='string' or redis.call('PTTL',KEYS[1])~=-1 then return 0 end
if redis.call('GET',KEYS[1])==ARGV[1] then return 1 end
return 0`,[]string{ordinaryBadgeEpoch+scope},control).Int()
	if err!=nil || result!=1{return ErrOrdinaryBadgeUnprepared};return nil
}

func (l *OrdinaryBadgeLedger) CheckReady(ctx context.Context)error{
	if l==nil || l.rdb==nil || ctx==nil{return ErrOrdinaryBadgeUnprepared}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	result,err:=l.rdb.Eval(current,`if redis.call('TYPE',KEYS[1]).ok~='string' or redis.call('PTTL',KEYS[1])~=-1 then return 0 end
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
return 1`,[]string{ordinaryBadgeEpoch+l.scope},l.control).Int()
	if err!=nil || result!=1{return ErrOrdinaryBadgeUnprepared};return nil
}

type OrdinaryBadgeSnapshot struct {
	Value int
	ResetID,ReceiptSHA256 string
}

func badgeHash(body []byte)string{sum:=sha256.Sum256(body);return hex.EncodeToString(sum[:])}
func badgeSHA(v string)bool{b,e:=hex.DecodeString(v);return e==nil && len(b)==32 && hex.EncodeToString(b)==v}
func badgeUUID(v string)bool{id,e:=uuid.Parse(v);return e==nil && id!=uuid.Nil && id.String()==v}
func badgeUser(v string)bool{return v!="" && len(v)<=512 && strings.TrimSpace(v)==v && !strings.ContainsAny(v,"\x00\r\n")}
func badgeNonce()(string,error){var b [16]byte;if _,err:=rand.Read(b[:]);err!=nil{return "",ErrOrdinaryBadgeUnprepared};return hex.EncodeToString(b[:]),nil}

func (l *OrdinaryBadgeLedger) keys(user,branch string)[]string{
	return []string{userBadgeUnreadCountSum+user,ordinaryBadgeOwner+user,ordinaryBadgeEpoch+l.scope}
}

const ordinaryBadgeReserveLua=`
local function typ(k) return redis.call('TYPE',k).ok end
local function count(v)
 if not v or not string.match(v,'^%d+$') or (#v>1 and string.sub(v,1,1)=='0') then return nil end
 if #v>10 or (#v==10 and v>'2147483647') then return nil end
 return tonumber(v)
end
if typ(KEYS[3])~='string' or redis.call('PTTL',KEYS[3])~=-1 or redis.call('GET',KEYS[3])~=ARGV[3] then return {} end
local ct=typ(KEYS[1]);if ct~='none' and ct~='string' then return {} end
if ct~='none' and redis.call('PTTL',KEYS[1])~=-1 then return {} end
local value=ct=='none' and '0' or redis.call('GET',KEYS[1])
local owner=typ(KEYS[2]);if owner~='none' and owner~='hash' then return {} end
local reset=ARGV[4]
if owner=='hash' then
 if redis.call('PTTL',KEYS[2])~=-1 or redis.call('HGET',KEYS[2],'scope')~=ARGV[1] or redis.call('HGET',KEYS[2],'epoch')~=ARGV[2] or redis.call('HGET',KEYS[2],'control')~=ARGV[3] then return {} end
 reset=redis.call('HGET',KEYS[2],'reset');if not reset or #reset~=32 or not string.match(reset,'^[0-9a-f]+$') then return {} end
 value=redis.call('HGET',KEYS[2],'counter')
end
local current=count(value);if not current then return {} end
local receipt=owner=='hash' and redis.call('HGET',KEYS[2],ARGV[5]) or false
if receipt then
 redis.call('SET',KEYS[1],value)
 return {value,reset,receipt}
end
if ARGV[6]~='increment' and ARGV[6]~='observe' then return {} end
if ARGV[6]=='increment' and current>=2147483647 then return {} end
-- All schema/epoch/value checks finish before the first write.
local next=value;if ARGV[6]=='increment' then next=tostring(current+1) end
receipt=ARGV[1]..'|'..ARGV[2]..'|'..reset..'|'..next..'|'..ARGV[6]
-- Accounting and dedup are ONE Redis command. A later projection SET error
-- cannot leave an increment without its receipt (Lua does not roll back).
redis.call('HSET',KEYS[2],'scope',ARGV[1],'epoch',ARGV[2],'control',ARGV[3],'reset',reset,'counter',next,ARGV[5],receipt)
redis.call('SET',KEYS[1],next)
return {next,reset,receipt}
`

// Reserve raises this user's existing badge counter once per exact branch.
// Replaying a lost EVAL ACK reads the same receipt. After SetAppBadge reset it
// does not re-add the old message; it returns the current count/reset instead.
// This guarantees the Redis accounting effect, not ordered device delivery.
func (l *OrdinaryBadgeLedger) Reserve(ctx context.Context,user,branch string)(OrdinaryBadgeSnapshot,error){
	return l.Freeze(ctx,user,branch,true)
}

// IOSBadgeCount=false observes the counter without incrementing it. The FCM
// adapter retains the legacy zero/missing fallback of badge=1 separately;
// turning a read-only badge policy into an increment is not permitted.
func (l *OrdinaryBadgeLedger) Freeze(ctx context.Context,user,branch string,increment bool)(OrdinaryBadgeSnapshot,error){
	if l==nil || ctx==nil || !badgeUser(user) || !badgeUUID(branch){return OrdinaryBadgeSnapshot{},ErrOrdinaryBadgeUnprepared}
	nonce,err:=badgeNonce();if err!=nil{return OrdinaryBadgeSnapshot{},err}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	mode:="observe";if increment{mode="increment"}
	rows,err:=l.rdb.Eval(current,ordinaryBadgeReserveLua,l.keys(user,branch),l.scope,l.epoch,l.control,nonce,"r:"+branch,mode).Slice()
	if err!=nil || len(rows)!=3{return OrdinaryBadgeSnapshot{},ErrOrdinaryBadgeUnprepared}
	value,valueOK:=rows[0].(string);reset,resetOK:=rows[1].(string);receipt,receiptOK:=rows[2].(string)
	parts:=strings.Split(receipt,"|")
	if !valueOK || !resetOK || !receiptOK || len(reset)!=32 || !badgeSHA(reset+reset) || len(parts)!=5 || parts[0]!=l.scope || parts[1]!=l.epoch || len(parts[2])!=32 || !badgeSHA(parts[2]+parts[2]) || parts[4]!=mode{return OrdinaryBadgeSnapshot{},ErrOrdinaryBadgeUnprepared}
	number,err:=strconv.ParseInt(value,10,32)
	original,originalErr:=strconv.ParseInt(parts[3],10,32)
	if err!=nil || originalErr!=nil || number<0 || original<0 || mode=="increment" && original==0 || strconv.FormatInt(number,10)!=value || strconv.FormatInt(original,10)!=parts[3]{return OrdinaryBadgeSnapshot{},ErrOrdinaryBadgeUnprepared}
	return OrdinaryBadgeSnapshot{Value:int(number),ResetID:reset,ReceiptSHA256:badgeHash([]byte(receipt))},nil
}

// Final reset-generation validation is performed before a provider is called.
// Another message may legitimately raise the same counter; this is not treated
// as a reset or used to allocate another increment for the old branch.
func (l *OrdinaryBadgeLedger) Validate(ctx context.Context,user,branch string,snapshot OrdinaryBadgeSnapshot)error{
	if l==nil || ctx==nil || !badgeUser(user) || !badgeUUID(branch) || snapshot.Value<0 || int64(snapshot.Value)>2147483647 || len(snapshot.ResetID)!=32 || !badgeSHA(snapshot.ResetID+snapshot.ResetID) || !badgeSHA(snapshot.ReceiptSHA256){return ErrOrdinaryBadgeUnprepared}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	rows,err:=l.rdb.Eval(current,`if redis.call('TYPE',KEYS[3]).ok~='string' or redis.call('PTTL',KEYS[3])~=-1 or redis.call('GET',KEYS[3])~=ARGV[3] then return {} end
if redis.call('TYPE',KEYS[2]).ok~='hash' or redis.call('PTTL',KEYS[2])~=-1 then return {} end
if redis.call('HGET',KEYS[2],'scope')~=ARGV[1] or redis.call('HGET',KEYS[2],'epoch')~=ARGV[2] or redis.call('HGET',KEYS[2],'control')~=ARGV[3] or redis.call('HGET',KEYS[2],'reset')~=ARGV[4] then return {} end
local receipt=redis.call('HGET',KEYS[2],ARGV[5]);if not receipt then return {} end
return {receipt}`,l.keys(user,branch),l.scope,l.epoch,l.control,snapshot.ResetID,"r:"+branch).Slice()
	if err!=nil || len(rows)!=1{return ErrOrdinaryBadgeUnprepared}
	receipt,ok:=rows[0].(string);if !ok || badgeHash([]byte(receipt))!=snapshot.ReceiptSHA256{return ErrOrdinaryBadgeUnprepared};return nil
}

// The existing ThirdDatabase.SetAppBadge -> MsgModel setter really reaches
// this atomic reset fence. With no activated owner ledger its SET semantics
// are unchanged. An active ledger changes the reset nonce in the SAME script
// as the count, so a retry cannot re-increment an already-accounted branch.
func (c *msgCache) setBadgeWithOrdinaryResetFence(ctx context.Context,user string,value int)error{
	if ctx==nil || !badgeUser(user){return ErrOrdinaryBadgeUnprepared}
	nonce,err:=badgeNonce();if err!=nil{return err}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	result,err:=c.rdb.Eval(current,`local t=redis.call('TYPE',KEYS[2]).ok
if t=='none' then redis.call('SET',KEYS[1],ARGV[1]);return 1 end
if t~='hash' or redis.call('PTTL',KEYS[2])~=-1 then return 0 end
local scope=redis.call('HGET',KEYS[2],'scope');local control=redis.call('HGET',KEYS[2],'control')
if not scope or #scope~=64 or not string.match(scope,'^[0-9a-f]+$') or not control then return 0 end
local global=ARGV[3]..scope
if redis.call('TYPE',global).ok~='string' or redis.call('PTTL',global)~=-1 or redis.call('GET',global)~=control then return 0 end
if not string.match(ARGV[1],'^%d+$') or #ARGV[1]>10 or (#ARGV[1]==10 and ARGV[1]>'2147483647') then return 0 end
redis.call('HSET',KEYS[2],'reset',ARGV[2],'counter',ARGV[1]);redis.call('SET',KEYS[1],ARGV[1]);return 1`,[]string{userBadgeUnreadCountSum+user,ordinaryBadgeOwner+user},strconv.Itoa(value),nonce,ordinaryBadgeEpoch).Int()
	if err!=nil || result!=1{return ErrOrdinaryBadgeUnprepared};return nil
}

// Old aggregate FCM may not bypass the receipt ledger once a user is bound.
// Its pre-activation legacy semantics remain available for unbound users.
func (c *msgCache) incrBadgeWithoutOrdinaryBypass(ctx context.Context,user string)(int,error){
	if ctx==nil || !badgeUser(user){return 0,ErrOrdinaryBadgeUnprepared}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	rows,err:=c.rdb.Eval(current,`if redis.call('TYPE',KEYS[2]).ok~='none' then return {} end
redis.call('INCR',KEYS[1]);return {redis.call('GET',KEYS[1])}`,[]string{userBadgeUnreadCountSum+user,ordinaryBadgeOwner+user}).Slice()
	if err!=nil || len(rows)!=1{return 0,ErrOrdinaryBadgeUnprepared}
	value,ok:=rows[0].(string);if !ok{return 0,ErrOrdinaryBadgeUnprepared}
	number,err:=strconv.Atoi(value);if err!=nil{return 0,ErrOrdinaryBadgeUnprepared};return number,nil
}

func (c *msgCache) getBadgeWithOrdinaryAuthority(ctx context.Context,user string)(int,error){
	if ctx==nil || !badgeUser(user){return 0,ErrOrdinaryBadgeUnprepared}
	current,cancel:=context.WithTimeout(ctx,3*time.Second);defer cancel()
	rows,err:=c.rdb.Eval(current,`local t=redis.call('TYPE',KEYS[2]).ok
if t=='none' then local value=redis.call('GET',KEYS[1]);if not value then return {'missing'} end;return {'legacy',value} end
if t~='hash' or redis.call('PTTL',KEYS[2])~=-1 then return {} end
local scope=redis.call('HGET',KEYS[2],'scope');local control=redis.call('HGET',KEYS[2],'control')
if not scope or #scope~=64 or not string.match(scope,'^[0-9a-f]+$') or not control then return {} end
local global=ARGV[1]..scope
if redis.call('TYPE',global).ok~='string' or redis.call('PTTL',global)~=-1 or redis.call('GET',global)~=control then return {} end
local value=redis.call('HGET',KEYS[2],'counter');if not value then return {} end
return {'owned',value}`,[]string{userBadgeUnreadCountSum+user,ordinaryBadgeOwner+user},ordinaryBadgeEpoch).Slice()
	if err!=nil || len(rows)<1{return 0,ErrOrdinaryBadgeUnprepared}
	if len(rows)==1 && rows[0]=="missing"{return 0,redis.Nil}
	if len(rows)!=2 || rows[0]!="legacy" && rows[0]!="owned"{return 0,ErrOrdinaryBadgeUnprepared}
	value,ok:=rows[1].(string);if !ok{return 0,ErrOrdinaryBadgeUnprepared}
	number,err:=strconv.Atoi(value);if err!=nil{return 0,ErrOrdinaryBadgeUnprepared}
	if rows[0]=="owned" && (number<0 || int64(number)>2147483647){return 0,ErrOrdinaryBadgeUnprepared};return number,nil
}
