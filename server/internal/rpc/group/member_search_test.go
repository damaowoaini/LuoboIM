package group

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/pagination"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
)

type memberSearchDB struct {
	controller.GroupDatabase
	memberAllowed bool
	guardCalls    int
	exactCalls    int
	pageCalls     int
	allCalls      int
	groupID       string
	keyword       string
	page          pagination.Pagination
	total         int64
	members       []*relation.GroupMemberModel
	failure       error
}

func (d *memberSearchDB) TakeGroupMember(context.Context, string, string) (*relation.GroupMemberModel, error) {
	d.guardCalls++
	if !d.memberAllowed {
		return nil, errs.ErrRecordNotFound.Wrap("synthetic outsider")
	}
	return &relation.GroupMemberModel{UserID: "fixture-member", RoleLevel: constant.GroupOrdinaryUsers}, nil
}

func (d *memberSearchDB) SearchGroupMemberExact(_ context.Context, groupID, keyword string, page pagination.Pagination) (int64, []*relation.GroupMemberModel, error) {
	d.exactCalls++
	d.groupID, d.keyword, d.page = groupID, keyword, page
	return d.total, d.members, d.failure
}

func (d *memberSearchDB) PageGetGroupMember(_ context.Context, groupID string, page pagination.Pagination) (int64, []*relation.GroupMemberModel, error) {
	d.pageCalls++
	d.groupID, d.page = groupID, page
	return d.total, d.members, d.failure
}

func (d *memberSearchDB) FindGroupMemberAll(context.Context, string) ([]*relation.GroupMemberModel, error) {
	d.allCalls++
	return nil, errors.New("unbounded member hydration must not be called")
}

func memberSearchServer(d *memberSearchDB, hydrated *[]string) *groupServer {
	s := &groupServer{db: d}
	s.Notification = notification.NewGroupNotificationSender(d, &rpcclient.MessageRpcClient{}, &rpcclient.UserRpcClient{},
		func(_ context.Context, ids []string) ([]notification.CommonUser, error) {
			*hydrated = append(*hydrated, ids...)
			users := make([]notification.CommonUser, 0, len(ids))
			for _, id := range ids {
				users = append(users, &sdkws.UserInfo{UserID: id, Nickname: "global", FaceURL: "https://fixture.invalid/avatar"})
			}
			return users, nil
		})
	return s
}

func TestGroupMemberKeywordUsesDatabasePageAndHydratesOnlyThatPage(t *testing.T) {
	d := &memberSearchDB{memberAllowed: true, total: 10, members: []*relation.GroupMemberModel{
		{GroupID: "g", UserID: "page-a", Nickname: "精确", RoleLevel: constant.GroupOwner},
		{GroupID: "g", UserID: "page-b", Nickname: "精确", RoleLevel: constant.GroupAdmin},
		{GroupID: "g", UserID: "page-c", Nickname: "精确", RoleLevel: constant.GroupOrdinaryUsers},
	}}
	var hydrated []string
	s := memberSearchServer(d, &hydrated)
	page := &sdkws.RequestPagination{PageNumber: 2, ShowNumber: 3}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "fixture-member")
	resp, err := s.GetGroupMemberList(ctx, &pbgroup.GetGroupMemberListReq{GroupID: "g", Keyword: "精确", Filter: 1, Pagination: page})
	if err != nil || resp.Total != 10 || len(resp.Members) != 3 {
		t.Fatal("database total/page was lost or paginated twice", resp, err)
	}
	if d.guardCalls != 1 || d.exactCalls != 1 || d.pageCalls != 0 || d.allCalls != 0 || d.groupID != "g" || d.keyword != "精确" || d.page != page {
		t.Fatal("authorization or database dispatch changed")
	}
	if len(hydrated) != 3 {
		t.Fatal("profile hydration did not stay within the selected page")
	}
	for i, role := range []int32{constant.GroupOwner, constant.GroupAdmin, constant.GroupOrdinaryUsers} {
		if resp.Members[i].RoleLevel != role || resp.Members[i].Nickname != "精确" || resp.Members[i].FaceURL == "" {
			t.Fatal("page hydration changed an explicit nickname or existing role semantics")
		}
	}
}

func TestGroupMemberEmptyKeywordPreservesExistingPageOrder(t *testing.T) {
	d := &memberSearchDB{memberAllowed: true, total: 7, members: []*relation.GroupMemberModel{
		{UserID: "z-last-alphabetically", Nickname: "card", FaceURL: "face"},
		{UserID: "a-first-alphabetically", Nickname: "card", FaceURL: "face"},
	}}
	var hydrated []string
	s := memberSearchServer(d, &hydrated)
	ctx := context.WithValue(context.Background(), constant.OpUserID, "fixture-member")
	resp, err := s.GetGroupMemberList(ctx, &pbgroup.GetGroupMemberListReq{GroupID: "g", Pagination: &sdkws.RequestPagination{PageNumber: 2, ShowNumber: 2}})
	if err != nil || resp.Total != 7 || len(resp.Members) != 2 || resp.Members[0].UserID != "z-last-alphabetically" || resp.Members[1].UserID != "a-first-alphabetically" {
		t.Fatal("empty keyword path changed old order/pagination", resp, err)
	}
	if d.pageCalls != 1 || d.exactCalls != 0 || d.allCalls != 0 || len(hydrated) != 0 {
		t.Fatal("empty keyword no longer uses the existing page path")
	}
}

func TestGroupMemberSearchRejectsOutsiderBeforeQuery(t *testing.T) {
	for _, keyword := range []string{"", "exact"} {
		d := &memberSearchDB{}
		var hydrated []string
		ctx := context.WithValue(context.Background(), constant.OpUserID, "fixture-outsider")
		_, err := memberSearchServer(d, &hydrated).GetGroupMemberList(ctx, &pbgroup.GetGroupMemberListReq{GroupID: "g", Keyword: keyword})
		if err == nil || d.guardCalls != 1 || d.exactCalls+d.pageCalls+d.allCalls != 0 || len(hydrated) != 0 {
			t.Fatal("outsider reached member data", err)
		}
	}
}

func TestGroupMemberSearchAdminBoundaryAndDatabaseFailure(t *testing.T) {
	before := config.Config.IMAdmin.UserID
	config.Config.IMAdmin.UserID = append(append([]string(nil), before...), "fixture-search-admin")
	t.Cleanup(func() { config.Config.IMAdmin.UserID = before })
	ctx := context.WithValue(context.Background(), constant.OpUserID, "fixture-search-admin")
	d := &memberSearchDB{failure: errors.New("synthetic database failure")}
	var hydrated []string
	_, err := memberSearchServer(d, &hydrated).GetGroupMemberList(ctx, &pbgroup.GetGroupMemberListReq{GroupID: "g", Keyword: "exact"})
	if !errors.Is(err, d.failure) || d.guardCalls != 0 || d.exactCalls != 1 || d.allCalls != 0 || !reflect.DeepEqual(hydrated, []string(nil)) {
		t.Fatal("admin boundary or database error propagation changed", err)
	}
}
