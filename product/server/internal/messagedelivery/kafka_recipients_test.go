package messagedelivery

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

func kafkaTips(t *testing.T,tips any)[]byte{
	t.Helper();detail,err:=json.Marshal(tips);if err!=nil{t.Fatal(err)}
	body,err:=json.Marshal(&sdkws.NotificationElem{Detail:string(detail)});if err!=nil{t.Fatal(err)};return body
}
func TestKafkaRecipientBranchesKeepLegalRemovedUserHintWithoutWideningVisibleGraybar(t *testing.T){
	roster:=&kafkaRoster{groupID:"g",users:[]string{"admin","member","owner"},roles:map[string]int32{"owner":constant.GroupOwner,"admin":constant.GroupAdmin,"member":constant.GroupOrdinaryUsers}}
	for _,kind:=range []string{"kick","quit"}{
		t.Run(kind,func(t *testing.T){
			m:=&sdkws.MsgData{MsgFrom:constant.SysMsgType,GroupID:"g",SessionType:constant.SuperGroupChatType,AtUserIDList:[]string{"owner","admin","not-a-current-member"}}
			left:=&sdkws.GroupMemberFullInfo{GroupID:"g",UserID:"removed"}
			if kind=="kick"{m.ContentType=constant.MemberKickedNotification;m.Content=kafkaTips(t,&sdkws.MemberKickedTips{Group:&sdkws.GroupInfo{GroupID:"g"},KickedUserList:[]*sdkws.GroupMemberFullInfo{left}})}else{m.ContentType=constant.MemberQuitNotification;m.Content=kafkaTips(t,&sdkws.MemberQuitTips{Group:&sdkws.GroupInfo{GroupID:"g"},QuitUser:left})}
			m.Options=msgprocessor.NewOptions(msgprocessor.WithNotNotification(true))
			visible,err:=roster.recipients(m);if err!=nil || !reflect.DeepEqual(visible,[]string{"admin","owner"}){t.Fatal("visible graybar escaped exact current AtUser intersection",visible,err)}
			m.Options=msgprocessor.NewOptions(msgprocessor.WithNotNotification(false))
			silent,err:=roster.recipients(m);if err!=nil || !reflect.DeepEqual(silent,[]string{"admin","member","owner","removed"}){t.Fatal("silent n_ hint omitted the legally removed/quit user",silent,err)}
		})
	}
}

func TestKafkaRecipientModerationUsesFrozenAuthoritativeRolesAndExactTarget(t *testing.T){
	roster:=&kafkaRoster{groupID:"g",users:[]string{"admin","member","owner"},roles:map[string]int32{"owner":constant.GroupOwner,"admin":constant.GroupAdmin,"member":constant.GroupOrdinaryUsers}}
	m:=&sdkws.MsgData{MsgFrom:constant.SysMsgType,GroupID:"g",SessionType:constant.SuperGroupChatType,ContentType:constant.GroupMemberMutedNotification,Content:kafkaTips(t,&sdkws.GroupMemberMutedTips{Group:&sdkws.GroupInfo{GroupID:"g"},MutedUser:&sdkws.GroupMemberFullInfo{GroupID:"g",UserID:"member"}})}
	got,err:=roster.recipients(m);if err!=nil || !reflect.DeepEqual(got,[]string{"admin","member","owner"}){t.Fatal("current admins or exact muted user omitted",got,err)}
	roster.roles["admin"]=constant.GroupOrdinaryUsers
	got,err=roster.recipients(m);if err!=nil || !reflect.DeepEqual(got,[]string{"member","owner"}){t.Fatal("stale cached role was substituted for authoritative snapshot",got,err)}
	m.Content=kafkaTips(t,&sdkws.GroupMemberMutedTips{Group:&sdkws.GroupInfo{GroupID:"other"},MutedUser:&sdkws.GroupMemberFullInfo{GroupID:"other",UserID:"foreign"}})
	if _,err:=roster.recipients(m);!errors.Is(err,ErrInvalid){t.Fatal("foreign tips supplied extra recipient authority",err)}
}
