package messagedelivery

import (
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

func TestMembershipReaderMongoPageRequiresEveryExactOwnerAndPreparedSchema(t *testing.T) {
	ctx,_,base,db,_:=membershipFixture(t,nil)
	s,err:=NewMembershipBoundStore(db,base.epoch,base.activationSHA)
	if err!=nil { t.Fatal(err) }
	rows,err:=s.ReadMembershipWindows(ctx,"sg_g",[]string{"member","owner"})
	if err!=nil || len(rows)!=2 || rows["member"].Closed { t.Fatal("prepared page rejected",err) }
	if _,err:=s.ReadMembershipWindows(ctx,"sg_g",[]string{"member","missing"});!errors.Is(err,ErrUnprepared) { t.Fatal("missing owner was inferred open",err) }
	if _,err:=s.ReadMembershipWindows(ctx,"sg_g",[]string{"member","member"});!errors.Is(err,ErrInvalid) { t.Fatal("duplicate owner page accepted",err) }
	if _,err:=s.ReadMembershipWindows(ctx,"n_g",[]string{"member"});!errors.Is(err,ErrInvalid) { t.Fatal("notification CID borrowed a group window",err) }
	if _,err:=db.Collection(Sequences).UpdateOne(ctx,bson.M{"_id":rows["member"].ID},bson.M{"$unset":bson.M{"closed":""}});err!=nil { t.Fatal(err) }
	if _,err:=s.ReadMembershipWindows(ctx,"sg_g",[]string{"member","owner"});!errors.Is(err,ErrConflict) { t.Fatal("malformed page was partially accepted",err) }
	if _,err:=db.Collection(Receipts).DeleteOne(ctx,bson.M{"_id":MembershipSchemaID});err!=nil { t.Fatal(err) }
	if err:=s.CheckReady(ctx);err==nil { t.Fatal("strict instance accepted missing migration marker") }
}

func TestMembershipReceiverMongoSuppressesClosedZeroWithoutStarvingOtherRecipient(t *testing.T) {
	ctx,authority,base,db,members:=membershipFixture(t,nil)
	if _,_,err:=removeAtBoundary(ctx,authority,base,db,members,nil);err!=nil { t.Fatal(err) }
	s,err:=NewMembershipBoundStore(db,base.epoch,base.activationSHA)
	if err!=nil { t.Fatal(err) }
	e:=membershipOrdinaryEnvelope(t)
	if _,err:=s.AcceptCommitted(ctx,e);err!=nil { t.Fatal(err) }
	if err:=s.Project(ctx,e.Branches[0].ID,discardProjection{});err!=nil { t.Fatal(err) }
	var message sdkws.MsgData
	if err:=proto.Unmarshal(e.Branches[0].Payload,&message);err!=nil { t.Fatal(err) };message.Seq=1
	// Deliberately stale body reader: the receiver itself must apply the actual
	// same-TX member-removal window, even if a caller supplies an old reader.
	reader:=&staleBodyReader{message:&message}
	transport:=&recordingOnline{logical:map[string]bool{}}
	if err:=s.RecoverOnline(ctx,e.Branches[0].ID,reader,transport);err!=nil { t.Fatal(err) }
	if len(transport.calls)!=1 || len(transport.calls[0].Recipients)!=1 || transport.calls[0].Recipients[0]!="owner" { t.Fatal("closed-zero member received future body or blocked the remaining owner") }
	restarted,err:=NewMembershipBoundStore(db,base.epoch,base.activationSHA)
	if err!=nil { t.Fatal(err) }
	if err:=restarted.RecoverOnline(ctx,e.Branches[0].ID,reader,transport);err!=nil || len(transport.calls)!=1 { t.Fatal("completed frozen page replayed after restart",err) }
}

func TestMembershipReceiverMongoRechecksAuthorityAfterDelayedBodyReader(t *testing.T) {
	ctx,_,base,db,_:=membershipFixture(t,nil)
	s,err:=NewMembershipBoundStore(db,base.epoch,base.activationSHA)
	if err!=nil { t.Fatal(err) }
	e:=membershipOrdinaryEnvelope(t)
	if _,err:=s.AcceptCommitted(ctx,e);err!=nil { t.Fatal(err) }
	if err:=s.Project(ctx,e.Branches[0].ID,discardProjection{});err!=nil { t.Fatal(err) }
	var message sdkws.MsgData
	if err:=proto.Unmarshal(e.Branches[0].Payload,&message);err!=nil { t.Fatal(err) };message.Seq=1
	reader:=&staleBodyReader{message:&message,beforeReply:func() error {
		// Exact owned synthetic fixture fault: a required owner-window read now
		// becomes unprepared. This is not a claimed Kick/remote-revoke race test.
		_,err:=db.Collection(Sequences).DeleteOne(ctx,bson.M{"_id":key(s.epoch,"membership-window","sg_g\x00member")})
		return err
	}}
	transport:=&recordingOnline{logical:map[string]bool{}}
	if err:=s.RecoverOnline(ctx,e.Branches[0].ID,reader,transport);err==nil || len(transport.calls)!=0 { t.Fatal("final admission reused authority captured before an await") }
	d,err:=s.readDuty(ctx,e.Branches[0].ID)
	if err!=nil || d.OnlineClaim!="" || !d.OnlinePending { t.Fatal("failed final authority left an indefinite in-flight claim",err) }
}
