package msgprocessor

import (
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
)

func TestSharedStorageSplitPreservesOrdinaryAndSilentNotificationContract(t *testing.T){
	t.Run("ordinary-history",func(t *testing.T){
		m:=&sdkws.MsgData{Options:NewOptions(WithNotNotification(true),WithHistory(true))}
		stored,ephemeral,tips,silent,modifiers:=SplitStorageMessages([]*sdkws.MsgData{m})
		if len(stored)!=1 || stored[0]!=m || len(ephemeral)+len(tips)+len(silent)+len(modifiers)!=0{t.Fatal("ordinary history branch changed")}
	})
	t.Run("ordinary-nonhistory",func(t *testing.T){
		m:=&sdkws.MsgData{Options:NewOptions(WithNotNotification(true),WithHistory(false))}
		stored,ephemeral,tips,silent,modifiers:=SplitStorageMessages([]*sdkws.MsgData{m})
		if len(ephemeral)!=1 || ephemeral[0]!=m || len(stored)+len(tips)+len(silent)+len(modifiers)!=0{t.Fatal("ephemeral ordinary was persisted")}
	})
	t.Run("visible-copy-and-seqzero-tip",func(t *testing.T){
		m:=&sdkws.MsgData{GroupID:"g",SessionType:constant.SuperGroupChatType,Seq:0,Options:NewOptions(WithNotNotification(false),WithSendMsg(true),WithHistory(false),WithOfflinePush(true),WithUnreadCount(true))}
		stored,ephemeral,tips,silent,modifiers:=SplitStorageMessages([]*sdkws.MsgData{m})
		if len(stored)!=1 || stored[0]==m || len(silent)!=1 || silent[0]!=m || len(ephemeral)+len(tips)+len(modifiers)!=0{t.Fatal("visible clone and original silent tip were collapsed")}
		if GetConversationIDByMsg(stored[0])!="sg_g" || !Options(stored[0].Options).IsHistory() || !Options(stored[0].Options).IsOfflinePush() || !Options(stored[0].Options).IsUnreadCount(){t.Fatal("visible history/unread/offline copy changed")}
		if GetConversationIDByMsg(m)!="n_g" || Options(m.Options).IsHistory() || Options(m.Options).IsOfflinePush() || Options(m.Options).IsUnreadCount() || m.Seq!=0{t.Fatal("original n_ branch gained history/sequence or duplicate unread/offline effect")}
	})
	t.Run("notification-history-without-visible-copy",func(t *testing.T){
		m:=&sdkws.MsgData{GroupID:"g",SessionType:constant.SuperGroupChatType,Options:NewOptions(WithNotNotification(false),WithSendMsg(false),WithHistory(true))}
		stored,ephemeral,tips,silent,modifiers:=SplitStorageMessages([]*sdkws.MsgData{m})
		if len(tips)!=1 || tips[0]!=m || GetConversationIDByMsg(tips[0])!="n_g" || len(stored)+len(ephemeral)+len(silent)+len(modifiers)!=0{t.Fatal("stored n_ branch became a visible chat copy")}
	})
	t.Run("reaction-keeps-separate-modifier-duty",func(t *testing.T){
		for _,contentType:=range []int32{constant.ReactionMessageModifier,constant.ReactionMessageDeleter}{
			m:=&sdkws.MsgData{ContentType:contentType,Options:NewOptions(WithNotNotification(true),WithHistory(true))}
			stored,_,_,_,modifiers:=SplitStorageMessages([]*sdkws.MsgData{m})
			if len(stored)!=1 || len(modifiers)!=1 || stored[0]!=m || modifiers[0]!=m{t.Fatal("modifier side effect was discarded")}
		}
	})
}
