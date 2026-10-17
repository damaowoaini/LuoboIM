package office

import (
	"context"
	constant2 "github.com/OpenIMSDK/chat/pkg/common/constant"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	"github.com/OpenIMSDK/chat/pkg/proto/office"
	"github.com/OpenIMSDK/tools/utils"
)

func (o *officeServer) DbWorkMomentUserIDs(workMoment *table.WorkMoment) []string {
	userIDs := make([]string, 0, 1+len(workMoment.LikeUsers)+len(workMoment.AtUserIDs)+len(workMoment.PermissionUserIDs)+len(workMoment.Comments)*2)
	userIDs = append(userIDs, workMoment.UserID)
	for _, user := range workMoment.LikeUsers {
		userIDs = append(userIDs, user.UserID)
	}
	userIDs = append(userIDs, workMoment.AtUserIDs...)
	userIDs = append(userIDs, workMoment.PermissionUserIDs...)
	for _, comment := range workMoment.Comments {
		userIDs = append(userIDs, comment.UserID, comment.ReplyUserID)
	}
	return userIDs
}

func (o *officeServer) DbToPbWorkMomentComment(comment *table.Comment, userMap map[string]*common.UserPublicInfo) *office.Comment {
	pbComment := &office.Comment{
		CommentID:   comment.CommentID,
		UserID:      comment.UserID,
		ReplyUserID: comment.ReplyUserID,
		Content:     comment.Content,
		CreateTime:  comment.CreateTime.UnixMilli(),
	}
	if user := userMap[comment.UserID]; user != nil {
		pbComment.Nickname = user.Nickname
		pbComment.FaceURL = user.FaceURL
	}
	if user := userMap[comment.ReplyUserID]; user != nil {
		pbComment.ReplyNickname = user.Nickname
		pbComment.ReplyFaceURL = user.FaceURL
	}
	return pbComment
}

func (o *officeServer) DbToPbWorkMoment(workMoment *table.WorkMoment, userMap map[string]*common.UserPublicInfo) *office.WorkMoment {
	pbWorkMoment := &office.WorkMoment{
		WorkMomentID:    workMoment.WorkMomentID.Hex(),
		UserID:          workMoment.UserID,
		Content:         o.DbToPbWorkMomentContent(workMoment.Content),
		LikeUsers:       make([]*office.LikeUserInfo, 0, len(workMoment.LikeUsers)),
		Comments:        make([]*office.Comment, 0, len(workMoment.Comments)),
		Permission:      workMoment.Permission,
		PermissionUsers: make([]*common.UserPublicInfo, 0, len(workMoment.PermissionUserIDs)),
		AtUsers:         make([]*common.UserPublicInfo, 0, len(workMoment.AtUserIDs)),
		CreateTime:      workMoment.CreateTime.UnixMilli(),
	}
	if user := userMap[workMoment.UserID]; user != nil {
		pbWorkMoment.Nickname = user.Nickname
		pbWorkMoment.FaceURL = user.FaceURL
	}
	likeUser := make(map[string]int64)
	for _, lu := range workMoment.LikeUsers {
		likeUser[lu.UserID] = lu.CreateTime.UnixMilli()
		if user := userMap[lu.UserID]; user != nil {
			pbWorkMoment.LikeUsers = append(pbWorkMoment.LikeUsers, &office.LikeUserInfo{
				UserID:   user.UserID,
				Account:  user.Account,
				Email:    user.Email,
				Nickname: user.Nickname,
				FaceURL:  user.FaceURL,
				Gender:   user.Gender,
				Level:    user.Level,
				LikeTime: likeUser[user.UserID],
			})
		}
	}
	for _, userID := range workMoment.AtUserIDs {
		if user := userMap[userID]; user != nil {
			pbWorkMoment.AtUsers = append(pbWorkMoment.AtUsers, user)
		}
	}
	for _, userID := range workMoment.PermissionUserIDs {
		if user := userMap[userID]; user != nil {
			pbWorkMoment.PermissionUsers = append(pbWorkMoment.PermissionUsers, user)
		}
	}
	for _, comment := range workMoment.Comments {
		pbWorkMoment.Comments = append(pbWorkMoment.Comments, o.DbToPbWorkMomentComment(comment, userMap))
	}
	return pbWorkMoment
}

func (o *officeServer) DbToPbWorkMomentContent(content *table.WorkMomentContent) *office.WorkMomentContent {
	res := &office.WorkMomentContent{
		Metas: make([]*office.Meta, 0, len(content.Metas)),
		Text:  content.Text,
		Type:  content.Type,
	}
	for _, meta := range content.Metas {
		res.Metas = append(res.Metas, &office.Meta{
			Thumb:    meta.Thumb,
			Original: meta.Original,
			Width:    meta.Width,
			Height:   meta.Height,
		})
	}
	return res
}

func (o *officeServer) FillWorkMoment(ctx context.Context, workMoment *table.WorkMoment) (*office.WorkMoment, error) {
	userMap, err := o.user.MapUserPublicInfo(ctx, o.DbWorkMomentUserIDs(workMoment))
	if err != nil {
		return nil, err
	}
	return o.DbToPbWorkMoment(workMoment, userMap), nil
}

func (o *officeServer) FillWorkMoments(ctx context.Context, workMoments []*table.WorkMoment) ([]*office.WorkMoment, error) {
	if len(workMoments) == 0 {
		return nil, nil
	}
	var userIDs []string
	for i := range workMoments {
		userIDs = append(userIDs, o.DbWorkMomentUserIDs(workMoments[i])...)
	}
	userMap, err := o.user.MapUserPublicInfo(ctx, utils.Distinct(userIDs))
	if err != nil {
		return nil, err
	}
	pbWorkMoments := make([]*office.WorkMoment, 0, len(workMoments))
	for i := range workMoments {
		pbWorkMoments = append(pbWorkMoments, o.DbToPbWorkMoment(workMoments[i], userMap))
	}
	return pbWorkMoments, nil
}

func (o *officeServer) FillWorkMomentLogs(ctx context.Context, workMoments []*table.WorkMoment) ([]*office.WorkMomentLog, error) {
	pbWorkMoments, err := o.FillWorkMoments(ctx, workMoments)
	if err != nil {
		return nil, err
	}
	pbWorkMomentLogs := make([]*office.WorkMomentLog, 0, len(workMoments))
	for i := range pbWorkMoments {
		pb := pbWorkMoments[i]
		elem := &office.WorkMomentLog{
			WorkMomentID:    pb.WorkMomentID,
			UserID:          pb.UserID,
			Nickname:        pb.Nickname,
			FaceURL:         pb.FaceURL,
			Content:         pb.Content,
			LikeUsers:       pb.LikeUsers,
			Comments:        pb.Comments,
			Permission:      pb.Permission,
			PermissionUsers: pb.PermissionUsers,
			AtUsers:         pb.AtUsers,
			CreateTime:      pb.CreateTime,
		}
		if len(elem.LikeUsers) > 0 {
			elem.Type = constant2.WorkMomentLogTypeLike
		} else if len(elem.Comments) > 0 {
			elem.Type = constant2.WorkMomentLogTypeComment
		} else {
			elem.Type = constant2.WorkMomentLogTypeAt
		}
		pbWorkMomentLogs = append(pbWorkMomentLogs, elem)
	}
	return pbWorkMomentLogs, nil
}
