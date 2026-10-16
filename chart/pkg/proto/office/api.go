package office

import "github.com/OpenIMSDK/tools/utils"

func (x *GetUserTagsResp) ApiFormat() {
	utils.InitSlice(&x.Tags)
	for i := range x.Tags {
		utils.InitSlice(&x.Tags[i].Users)
	}
}

func (x *GetTagSendLogsResp) ApiFormat() {
	utils.InitSlice(&x.TagSendLogs)
	for i := range x.TagSendLogs {
		utils.InitSlice(&x.TagSendLogs[i].Tags)
		utils.InitSlice(&x.TagSendLogs[i].Users)
	}
}

func (x *GetWorkMomentByIDResp) ApiFormat() {
	if x.WorkMoment != nil {
		utils.InitSlice(&x.WorkMoment.LikeUsers)
		utils.InitSlice(&x.WorkMoment.Comments)
		utils.InitSlice(&x.WorkMoment.PermissionUsers)
		utils.InitSlice(&x.WorkMoment.AtUsers)
		if x.WorkMoment.Content != nil {
			utils.InitSlice(&x.WorkMoment.Content.Metas)
		}
	}
}

func (x *GetUserSendWorkMomentsResp) ApiFormat() {
	for i := range x.WorkMoments {
		utils.InitSlice(&x.WorkMoments[i].LikeUsers)
		utils.InitSlice(&x.WorkMoments[i].Comments)
		utils.InitSlice(&x.WorkMoments[i].PermissionUsers)
		utils.InitSlice(&x.WorkMoments[i].AtUsers)
		if x.WorkMoments[i].Content != nil {
			utils.InitSlice(&x.WorkMoments[i].Content.Metas)
		}
	}
}

func (x *GetUserRecvWorkMomentsResp) ApiFormat() {
	for i := range x.WorkMoments {
		utils.InitSlice(&x.WorkMoments[i].LikeUsers)
		utils.InitSlice(&x.WorkMoments[i].Comments)
		utils.InitSlice(&x.WorkMoments[i].PermissionUsers)
		utils.InitSlice(&x.WorkMoments[i].AtUsers)
		if x.WorkMoments[i].Content != nil {
			utils.InitSlice(&x.WorkMoments[i].Content.Metas)
		}
	}
}

func (x *FindRelevantWorkMomentsResp) ApiFormat() {
	for i := range x.WorkMoments {
		utils.InitSlice(&x.WorkMoments[i].LikeUsers)
		utils.InitSlice(&x.WorkMoments[i].Comments)
		utils.InitSlice(&x.WorkMoments[i].PermissionUsers)
		utils.InitSlice(&x.WorkMoments[i].AtUsers)
		if x.WorkMoments[i].Content != nil {
			utils.InitSlice(&x.WorkMoments[i].Content.Metas)
		}
	}
}
