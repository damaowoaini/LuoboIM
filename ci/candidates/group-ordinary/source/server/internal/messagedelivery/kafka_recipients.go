package messagedelivery

import (
	"encoding/json"
	"sort"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

// Created only by the same-TX authoritative roster reader. This grants no
// member/request writer scope. It preserves the old Push2SuperGroup branch
// contract while freezing recipients at the source's FIRST committed intake.
type kafkaRoster struct {
	groupID string
	users []string
	roles map[string]int32
}

func (r *kafkaRoster) recipients(message *sdkws.MsgData)([]string,error){
	if r==nil || message==nil{return nil,ErrInvalid}
	result:=append([]string(nil),r.users...)
	if r.groupID=="" || message.MsgFrom!=constant.SysMsgType{return result,nil}
	if message.GroupID!=r.groupID{return nil,ErrConflict}
	var left []string
	var moderated string
	decode:=func(target any)error{
		var elem sdkws.NotificationElem
		if json.Unmarshal(message.Content,&elem)!=nil || json.Unmarshal([]byte(elem.Detail),target)!=nil{return ErrInvalid};return nil
	}
	switch message.ContentType{
	case constant.MemberQuitNotification:
		var tips sdkws.MemberQuitTips
		if decode(&tips)!=nil || tips.Group==nil || tips.Group.GroupID!=r.groupID || tips.QuitUser==nil || !identity(tips.QuitUser.UserID) || tips.QuitUser.GroupID!=r.groupID{return nil,ErrInvalid}
		left=[]string{tips.QuitUser.UserID}
	case constant.MemberKickedNotification:
		var tips sdkws.MemberKickedTips
		if decode(&tips)!=nil || tips.Group==nil || tips.Group.GroupID!=r.groupID || len(tips.KickedUserList)==0{return nil,ErrInvalid}
		for _,member:=range tips.KickedUserList{if member==nil || member.GroupID!=r.groupID || !identity(member.UserID){return nil,ErrInvalid};left=append(left,member.UserID)}
	case constant.GroupMemberMutedNotification:
		var tips sdkws.GroupMemberMutedTips
		if decode(&tips)!=nil || tips.Group==nil || tips.Group.GroupID!=r.groupID || tips.MutedUser==nil || tips.MutedUser.GroupID!=r.groupID || !identity(tips.MutedUser.UserID){return nil,ErrInvalid};moderated=tips.MutedUser.UserID
	case constant.GroupMemberCancelMutedNotification:
		var tips sdkws.GroupMemberCancelMutedTips
		if decode(&tips)!=nil || tips.Group==nil || tips.Group.GroupID!=r.groupID || tips.MutedUser==nil || tips.MutedUser.GroupID!=r.groupID || !identity(tips.MutedUser.UserID){return nil,ErrInvalid};moderated=tips.MutedUser.UserID
	}
	if moderated!=""{
		result=nil
		for _,uid:=range r.users{if r.roles[uid]==constant.GroupOwner || r.roles[uid]==constant.GroupAdmin{result=append(result,uid)}}
		result=append(result,moderated)
	}else{
		switch message.ContentType{
		case constant.MemberQuitNotification,constant.MemberKickedNotification,constant.MemberInvitedNotification,constant.MemberEnterNotification:
			if len(message.AtUserIDList)>0 && !msgprocessor.IsNotification(msgprocessor.GetConversationIDByMsg(message)){
				allowed:=map[string]bool{};for _,uid:=range message.AtUserIDList{if !identity(uid){return nil,ErrInvalid};allowed[uid]=true}
				result=nil;for _,uid:=range r.users{if allowed[uid]{result=append(result,uid)}}
			}else{result=append(result,left...)}
		}
	}
	// The removed/quit user's n_ hint remains legal even though that identity
// is absent from the current roster. This is read routing, never a grant to
// add that user back or modify another member. Delayed privacy is still a
// separate mandatory final dispatch check.
	seen:=map[string]bool{};out:=make([]string,0,len(result))
	for _,uid:=range result{if !identity(uid){return nil,ErrInvalid};if !seen[uid]{seen[uid]=true;out=append(out,uid)}}
	sort.Strings(out);return out,nil
}
