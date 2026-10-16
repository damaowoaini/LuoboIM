package api

import (
	"encoding/json"
	"github.com/OpenIMSDK/chat/internal/api/structs"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/office"
	constant2 "github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/checker"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

func NewOffice(cc grpc.ClientConnInterface) *Office {
	return &Office{officeClient: office.NewOfficeClient(cc), imApiCaller: apicall.NewCallerInterface()}
}

type Office struct {
	officeClient office.OfficeClient
	imApiCaller  apicall.CallerInterface
}

func (o *Office) GetUserTags(c *gin.Context) {
	var req office.GetUserTagsReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	tagsResp, err := o.officeClient.GetUserTags(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	var groupIDs []string
	for _, tag := range tagsResp.Tags {
		groupIDs = append(groupIDs, tag.GroupIDs...)
	}
	mapGroup := make(map[string]*sdkws.GroupInfo)
	if len(groupIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		groups, err := o.imApiCaller.FindGroupInfo(apiCtx, groupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		for _, group := range groups {
			mapGroup[group.GroupID] = group
		}
	}
	tags := make([]*structs.Tag, 0, len(tagsResp.Tags))
	for _, tag := range tagsResp.Tags {
		utils.InitSlice(&tag.Users)
		groups := make([]*sdkws.GroupInfo, 0, len(tag.GroupIDs))
		for _, groupID := range tag.GroupIDs {
			if group := mapGroup[groupID]; group != nil {
				groups = append(groups, group)
			}
		}
		tags = append(tags, &structs.Tag{
			TagID:   tag.TagID,
			TagName: tag.TagName,
			Users:   tag.Users,
			Groups:  groups,
		})
	}
	apiresp.GinSuccess(c, struct {
		Tags []*structs.Tag `json:"tags"`
	}{Tags: tags})
}

func (o *Office) CreateTag(c *gin.Context) {
	var req office.CreateTagReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	if len(req.GroupIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		groups, err := o.imApiCaller.FindGroupInfo(apiCtx, req.GroupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if len(groups) != len(req.GroupIDs) {
			apiresp.GinError(c, errs.ErrArgs.Wrap("groupIDs not found"))
			return
		}
	}
	tagResp, err := o.officeClient.CreateTag(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, tagResp)
}

func (o *Office) DeleteTag(c *gin.Context) {
	a2r.Call(office.OfficeClient.DeleteTag, o.officeClient, c)
}

func (o *Office) SetTag(c *gin.Context) {
	var req office.SetTagReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	if len(req.AddGroupIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		groups, err := o.imApiCaller.FindGroupInfo(apiCtx, req.AddGroupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if len(groups) != len(req.AddGroupIDs) {
			apiresp.GinError(c, errs.ErrArgs.Wrap("add groupIDs not found"))
			return
		}
	}
	tagResp, err := o.officeClient.SetTag(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, tagResp)
}

func (o *Office) SendMsg2Tag(c *gin.Context) {
	var req office.SendMsg2TagReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(req.Content), &content); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("content unmarshal failed "+err.Error()))
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	if len(req.GroupIDs) > 0 {
		groups, err := o.imApiCaller.FindGroupInfo(apiCtx, req.GroupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if len(groups) != len(req.GroupIDs) {
			apiresp.GinError(c, errs.ErrArgs.Wrap("groupIDs not found"))
			return
		}
	}
	sendResp, err := o.officeClient.SendMsg2Tag(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	userIDSet := make(map[string]struct{})
	for _, userID := range sendResp.RecvUserIDs {
		userIDSet[userID] = struct{}{}
	}
	for _, groupID := range sendResp.RecvGroupIDs {
		userIDs, err := o.imApiCaller.FindGroupMemberUserIDs(apiCtx, groupID)
		if err == nil {
			_, userType, authErr := ginOpUser(c)
			if authErr != nil {
				apiresp.GinError(c, authErr)
				return
			}
			if userType != constant.AdminUser && !utils.Contain(sendResp.SendUser.UserID, userIDs...) {
				apiresp.GinError(c, errs.ErrNoPermission.Wrap("只能向本人所在群组群发"))
				return
			}
			for _, userID := range userIDs {
				userIDSet[userID] = struct{}{}
			}
		} else {
			log.ZError(apiCtx, "find group member userIDs failed", err, "groupID", groupID)
			apiresp.GinError(c, err)
			return
		}
	}
	for userID := range userIDSet {
		resp, err := o.imApiCaller.SendMsg(apiCtx, &apicall.SendMsgReq{
			RecvID:           userID,
			SendID:           sendResp.SendUser.UserID,
			SenderNickname:   sendResp.SendUser.Nickname,
			SenderFaceURL:    sendResp.SendUser.FaceURL,
			SenderPlatformID: req.SenderPlatformID,
			Content:          content,
			ContentType:      constant2.Custom,
			SessionType:      constant2.SingleChatType,
		})
		if err != nil {
			log.ZError(apiCtx, "send msg failed", err, "sendUserID", sendResp.SendUser.UserID, "recvUserID", userID)
			continue
		}
		log.ZDebug(apiCtx, "send msg success", "sendUserID", sendResp.SendUser.UserID, "recvUserID", userID, "resp", resp)
	}
	apiresp.GinSuccess(c, nil)
}

func (o *Office) GetTagSendLogs(c *gin.Context) {
	var req office.GetTagSendLogsReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	sendResp, err := o.officeClient.GetTagSendLogs(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	var groupIDs []string
	for _, sendLog := range sendResp.TagSendLogs {
		groupIDs = append(groupIDs, sendLog.GroupIDs...)
		for _, tag := range sendLog.Tags {
			groupIDs = append(groupIDs, tag.GroupIDs...)
		}
	}
	mapGroup := make(map[string]*sdkws.GroupInfo)
	if len(groupIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		groups, err := o.imApiCaller.FindGroupInfo(apiCtx, groupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		for _, group := range groups {
			mapGroup[group.GroupID] = group
		}
	}
	logs := make([]*structs.TagSendLog, 0, len(sendResp.TagSendLogs))
	for _, sendLog := range sendResp.TagSendLogs {
		tags := make([]*structs.Tag, 0, len(sendLog.Tags))
		for _, tag := range sendLog.Tags {
			utils.InitSlice(&tag.Users)
			groups := make([]*sdkws.GroupInfo, 0, len(tag.GroupIDs))
			for _, groupID := range tag.GroupIDs {
				if group := mapGroup[groupID]; group != nil {
					groups = append(groups, group)
				}
			}
			utils.InitSlice(&tag.GroupIDs)
			tags = append(tags, &structs.Tag{
				TagID:      tag.TagID,
				TagName:    tag.TagName,
				Users:      tag.Users,
				Groups:     groups,
				CreateTime: tag.CreateTime,
			})
		}
		groups := make([]*sdkws.GroupInfo, 0, len(sendLog.GroupIDs))
		for _, groupID := range sendLog.GroupIDs {
			if group := mapGroup[groupID]; group != nil {
				groups = append(groups, group)
			}
		}
		logs = append(logs, &structs.TagSendLog{
			Id:       sendLog.Id,
			Tags:     tags,
			Users:    sendLog.Users,
			Groups:   groups,
			Content:  sendLog.Content,
			SendTime: sendLog.SendTime,
		})
	}
	apiresp.GinSuccess(c, &struct {
		TagSendLogs []*structs.TagSendLog `json:"tagSendLogs"`
	}{TagSendLogs: logs})
}

func (o *Office) DelTagSendLogs(c *gin.Context) {
	a2r.Call(office.OfficeClient.DelTagSendLog, o.officeClient, c)
}

func (o *Office) GetUserTagByID(c *gin.Context) {
	var req office.GetUserTagByIDReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	tagResp, err := o.officeClient.GetUserTagByID(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	var groups []*sdkws.GroupInfo
	if len(tagResp.Tag.GroupIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		groups, err = o.imApiCaller.FindGroupInfo(apiCtx, tagResp.Tag.GroupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
	}
	utils.InitSlice(&groups)
	utils.InitSlice(&tagResp.Tag.Users)
	apiresp.GinSuccess(c, &struct {
		Tag *structs.Tag `json:"tag"`
	}{Tag: &structs.Tag{
		TagID:      tagResp.Tag.TagID,
		TagName:    tagResp.Tag.TagName,
		Users:      tagResp.Tag.Users,
		Groups:     groups,
		CreateTime: tagResp.Tag.CreateTime,
	},
	})
}

func (o *Office) CreateOneWorkMoment(c *gin.Context) {
	var req office.CreateOneWorkMomentReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	actor, role, err := mctx.Check(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if req.UserID == "" {
		req.UserID = actor
	} else if err := mctx.CheckAdminOr(c, req.UserID); err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	if len(req.PermissionGroupIDs) > 0 {
		for _, groupID := range utils.Distinct(req.PermissionGroupIDs) {
			userIDs, err := o.imApiCaller.FindGroupMemberUserIDs(apiCtx, groupID)
			if err != nil {
				apiresp.GinError(c, err)
				return
			}
			if role != constant.AdminUser && !utils.Contain(actor, userIDs...) {
				apiresp.GinError(c, errs.ErrNoPermission.Wrap("不能使用未加入群的成员作为可见范围"))
				return
			}
			req.PermissionUserIDs = append(req.PermissionUserIDs, userIDs...)
		}
	}
	req.PermissionUserIDs = utils.Distinct(req.PermissionUserIDs)
	resp, err := o.officeClient.CreateOneWorkMoment(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	userSet := make(map[string]struct{})
	//userSet[req.UserID] = struct{}{}
	userSet[resp.WorkMoment.UserID] = struct{}{}
	for _, user := range resp.WorkMoment.AtUsers {
		userSet[user.UserID] = struct{}{}
	}
	o.workMomentSlice(resp.WorkMoment)
	for userID := range userSet {
		_ = o.imApiCaller.SendBusinessNotification(apiCtx, constant.WorkMomentAtNotification, resp.WorkMoment, req.UserID, userID)
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Office) DeleteOneWorkMoment(c *gin.Context) {
	var req office.DeleteOneWorkMomentReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	resp, err := o.officeClient.DeleteOneWorkMoment(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if len(resp.UserIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		for _, userID := range resp.UserIDs {
			_ = o.imApiCaller.SendBusinessNotification(apiCtx, constant.WorkMomentDeleteNotification, map[string]string{"workMomentID": req.WorkMomentID}, req.UserID, userID)
		}
	}
	apiresp.GinSuccess(c, nil)
}

func (o *Office) LikeOneWorkMoment(c *gin.Context) {
	var req office.LikeOneWorkMomentReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	if req.UserID == "" {
		req.UserID = mctx.GetOpUserID(c)
	}
	resp, err := o.officeClient.LikeOneWorkMoment(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	userSet := make(map[string]struct{})
	//userSet[req.UserID] = struct{}{}
	userSet[resp.WorkMoment.UserID] = struct{}{}
	for _, user := range resp.WorkMoment.AtUsers {
		userSet[user.UserID] = struct{}{}
	}
	o.workMomentSlice(resp.WorkMoment)
	for userID := range userSet {
		_ = o.imApiCaller.SendBusinessNotification(apiCtx, constant.WorkMomentLikeNotification, resp.WorkMoment, req.UserID, userID)
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Office) CommentOneWorkMoment(c *gin.Context) {
	var req office.CommentOneWorkMomentReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	if req.UserID == "" {
		req.UserID = mctx.GetOpUserID(c)
	}
	resp, err := o.officeClient.CommentOneWorkMoment(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	userSet := make(map[string]struct{})
	//userSet[req.UserID] = struct{}{}
	userSet[resp.WorkMoment.UserID] = struct{}{}
	for _, user := range resp.WorkMoment.AtUsers {
		userSet[user.UserID] = struct{}{}
	}
	if req.ReplyUserID != "" {
		userSet[req.ReplyUserID] = struct{}{}
	}
	o.workMomentSlice(resp.WorkMoment)
	for userID := range userSet {
		_ = o.imApiCaller.SendBusinessNotification(apiCtx, constant.WorkMomentCommentNotification, resp.WorkMoment, req.UserID, userID)
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Office) DeleteComment(c *gin.Context) {
	var req office.DeleteCommentReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	opUserID := mctx.GetOpUserID(c)
	resp, err := o.officeClient.DeleteComment(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	o.workMomentSlice(resp.WorkMoment)
	if len(resp.UserIDs) > 0 {
		imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiCtx := mctx.WithApiToken(c, imToken)
		for _, userID := range resp.UserIDs {
			_ = o.imApiCaller.SendBusinessNotification(apiCtx, constant.WorkMomentDeleteCommentNotification, resp.WorkMoment, opUserID, userID)
		}
	}
	apiresp.GinSuccess(c, nil)
}

func (o *Office) GetWorkMomentByID(c *gin.Context) {
	a2r.Call(office.OfficeClient.GetWorkMomentByID, o.officeClient, c)
}

func (o *Office) GetUserSendWorkMoments(c *gin.Context) {
	a2r.Call(office.OfficeClient.GetUserSendWorkMoments, o.officeClient, c)
}

func (o *Office) GetUserRecvWorkMoments(c *gin.Context) {
	var req office.GetUserRecvWorkMomentsReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	if req.UserID == "" {
		req.UserID = mctx.GetOpUserID(c)
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	req.FriendIDs, err = o.imApiCaller.FindFriendUserIDs(apiCtx, req.UserID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp, err := o.officeClient.GetUserRecvWorkMoments(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Office) GetUnreadWorkMoments(c *gin.Context) {
	a2r.Call(office.OfficeClient.FindRelevantWorkMoments, o.officeClient, c)
}

func (o *Office) GetUnreadWorkMomentsCount(c *gin.Context) {
	a2r.Call(office.OfficeClient.GetUnreadWorkMomentsCount, o.officeClient, c)
}

func (o *Office) ReadWorkMoments(c *gin.Context) {
	a2r.Call(office.OfficeClient.ReadWorkMoments, o.officeClient, c)
}

func (o *Office) workMomentSlice(workMoment *office.WorkMoment) {
	if workMoment == nil {
		return
	}
	if workMoment.Content == nil {
		workMoment.Content = &office.WorkMomentContent{}
	}
	utils.InitSlice(&workMoment.LikeUsers)
	utils.InitSlice(&workMoment.Comments)
	utils.InitSlice(&workMoment.PermissionUsers)
	utils.InitSlice(&workMoment.AtUsers)
	utils.InitSlice(&workMoment.Content.Metas)
}
