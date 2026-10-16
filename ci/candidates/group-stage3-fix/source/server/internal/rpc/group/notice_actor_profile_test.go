package group

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/mongo"
)

type noticeProfileAuthorityFixture struct {controller.GroupDatabase; row *relation.GroupMemberModel; err error}
func (f *noticeProfileAuthorityFixture) TakeGroupMemberForPermission(context.Context,string,string)(*relation.GroupMemberModel,error){return f.row,f.err}

func TestGroupNoticeProfileFallbackRequiresManagerAndActualMissingMember(t *testing.T) {
	fault:=errors.New("synthetic authoritative storage failure")
	for _,fixture:=range []struct{name,actor string; row *relation.GroupMemberModel; err error; hydrate bool}{
		{"manager-raw-missing","synthetic-service",nil,mongo.ErrNoDocuments,true},
		{"manager-wrapped-missing","synthetic-service",nil,fmt.Errorf("synthetic wrapped missing: %w",mongo.ErrNoDocuments),true},
		{"manager-code-missing","synthetic-service",nil,errs.ErrRecordNotFound,true},
		{"ordinary-not-a-member","outsider",nil,mongo.ErrNoDocuments,false},
		{"manager-storage-fault","synthetic-service",nil,fault,false},
		{"manager-foreign-row","synthetic-service",&relation.GroupMemberModel{GroupID:"other",UserID:"synthetic-service"},nil,false},
	} {
		t.Run(fixture.name,func(t *testing.T){
			memberEditSetup(t)
			users:=&statusManagerUsers{}
			service:=&groupServer{db:&noticeProfileAuthorityFixture{row:fixture.row,err:fixture.err}}
			service.User.Client=users
			profile,err:=service.noticeActorProfile(memberEditContext(fixture.actor),"g")
			if fixture.hydrate {
				if err!=nil || profile==nil || profile.UserID!=fixture.actor || users.calls!=1 {t.Fatal("legal manager missing-member hydration rejected or retargeted",err)}
			} else if err==nil || profile!=nil || users.calls!=0 {t.Fatal("non-manager/foreign row/storage fault became profile permission",err)}
			if fixture.err==fault && !errors.Is(err,fault) {t.Fatal("storage fault swallowed as a missing member",err)}
		})
	}
}

func TestGroupStatusRPCManagerMongoMissingMembershipStillCommitsOneNotice(t *testing.T) {
	ctx,service,_,db,_:=groupStatusFixture(t,nil,nil)
	row,err:=service.db.TakeGroupMemberForPermission(ctx,"g","synthetic-service")
	if row!=nil || !errors.Is(err,mongo.ErrNoDocuments) {t.Fatal("fixture did not prove real authoritative Mongo absence",err)}
	users:=&statusManagerUsers{}
	service.User.Client=users
	if _,err:=service.MuteGroup(statusActor(ctx,"synthetic-service"),&pbgroup.MuteGroupReq{GroupID:"g"});err!=nil {t.Fatal(err)}
	if users.calls!=1 || storedGroupStatus(t,ctx,db)!=constant.GroupStatusMuted || effectCount(t,ctx,db)!=1 {t.Fatal("legal missing-member manager did not commit exactly one notice")}
}
