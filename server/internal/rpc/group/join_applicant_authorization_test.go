package group

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

var joinApplicantWriteAdmission = errors.New("synthetic join writer admission sentinel")
var joinApplicantLookupFailure = errors.New("synthetic join user lookup failure")

// The test calls the real groupServer.JoinGroup and real UserRpcClient adapter.
// Only the remote user transport, group DB and Redis transport are fixtures.
// Stop at the exact writer before concrete notifications: positives establish
// authorized write admission, not actual membership commit or delivery.
type joinApplicantUserClient struct {
	pbuser.UserClient
	calls     int
	requested []string
	failure   error
}

func (u *joinApplicantUserClient) GetDesignateUsers(_ context.Context, req *pbuser.GetDesignateUsersReq, _ ...grpc.CallOption) (*pbuser.GetDesignateUsersResp, error) {
	u.calls++
	u.requested = append([]string(nil), req.UserIDs...)
	if u.failure != nil {
		return nil, u.failure
	}
	users := make([]*sdkws.UserInfo, 0, len(req.UserIDs))
	for _, id := range req.UserIDs {
		users = append(users, &sdkws.UserInfo{UserID: id, Nickname: "synthetic join applicant"})
	}
	return &pbuser.GetDesignateUsersResp{UsersInfo: users}, nil
}

type joinApplicantDB struct {
	controller.GroupDatabase
	groupReads  int
	memberReads int
	writes      int
	written     []*relation.GroupMemberModel
}

func (d *joinApplicantDB) TakeGroup(_ context.Context, groupID string) (*relation.GroupModel, error) {
	d.groupReads++
	return &relation.GroupModel{GroupID: groupID, GroupType: constant.WorkingGroup, NeedVerification: constant.Directly}, nil
}

func (d *joinApplicantDB) TakeGroupMember(context.Context, string, string) (*relation.GroupMemberModel, error) {
	d.memberReads++
	return nil, errs.ErrRecordNotFound.Wrap("synthetic applicant is not yet a member")
}

func (d *joinApplicantDB) CreateGroup(_ context.Context, groups []*relation.GroupModel, members []*relation.GroupMemberModel) error {
	d.writes++
	if len(groups) != 0 || len(members) != 1 || members[0] == nil {
		return errs.ErrArgs.Wrap("unexpected synthetic join write shape")
	}
	row := *members[0]
	d.written = append(d.written, &row)
	return joinApplicantWriteAdmission
}

type joinApplicantRedis struct {
	redis.UniversalClient
	reads int
}

func (r *joinApplicantRedis) Get(_ context.Context, key string) *redis.StringCmd {
	r.reads++
	if key != forbidNewMemberHistoryRedisKey {
		return redis.NewStringResult("", errs.ErrArgs.Wrap("unexpected synthetic Redis read"))
	}
	return redis.NewStringResult("false", nil)
}

type joinApplicantFixture struct {
	server    *groupServer
	users     *joinApplicantUserClient
	db        *joinApplicantDB
	redis     *joinApplicantRedis
	callbacks atomic.Int32
}

func newJoinApplicantFixture(t *testing.T) *joinApplicantFixture {
	t.Helper()
	before := config.Config
	t.Cleanup(func() { config.Config = before })
	config.Config.Manager.UserID = []string{"synthetic-join-manager"}
	config.Config.IMAdmin.UserID = []string{"synthetic-join-imadmin"}
	config.Config.Secret = "synthetic-join-callback-fixture-key"
	config.Config.Callback.CallbackBeforeMemberJoinGroup.Enable = false
	f := &joinApplicantFixture{users: &joinApplicantUserClient{}, db: &joinApplicantDB{}, redis: &joinApplicantRedis{}}
	// This in-process loopback callback is executed only by the cloud tests.
	// HMAC is checked without printing request/response bodies or signatures.
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(io.LimitReader(req.Body, 8193))
		command := strings.TrimPrefix(req.URL.Path, "/")
		mac := hmac.New(sha256.New, []byte(config.Config.Secret))
		mac.Write([]byte(req.Header.Get("X-OpenIM-Callback-Timestamp") + "\n" + req.Header.Get("X-OpenIM-Callback-Nonce") + "\n" + command + "\n"))
		mac.Write(body)
		signature, signatureErr := hex.DecodeString(req.Header.Get("X-OpenIM-Callback-Signature"))
		if err != nil || signatureErr != nil || len(body) > 8192 || len(req.Header.Get("X-OpenIM-Callback-Nonce")) != 32 || !hmac.Equal(mac.Sum(nil), signature) {
			http.Error(w, "synthetic callback authentication rejected", http.StatusUnauthorized)
			return
		}
		f.callbacks.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(callback.Close)
	config.Config.Callback.CallbackUrl = callback.URL
	config.Config.Callback.CallbackBeforeJoinGroup = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
	f.server = &groupServer{db: f.db, User: rpcclient.UserRpcClient{Client: f.users}, rdb: f.redis}
	return f
}

func joinApplicantContext(actor string) context.Context {
	return context.WithValue(context.WithValue(context.Background(), constant.OperationID, "synthetic-join-applicant"), constant.OpUserID, actor)
}

func TestJoinGroupRejectsForeignApplicantBeforeDependencies(t *testing.T) {
	cases := []struct{ name, actor, applicant string }{
		{"ordinary-foreign", "synthetic-ordinary", "synthetic-target"},
		{"group-admin-is-not-configured-manager", "synthetic-group-admin", "synthetic-target"},
		{"missing-actor", "", "synthetic-target"},
		{"missing-applicant", "synthetic-ordinary", ""},
		{"manager-missing-applicant", "synthetic-join-manager", ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			f := newJoinApplicantFixture(t)
			_, err := f.server.JoinGroup(joinApplicantContext(item.actor), &pbgroup.JoinGroupReq{GroupID: "synthetic-join-g", InviterUserID: item.applicant, JoinSource: constant.JoinBySearch})
			if !errs.ErrNoPermission.Is(err) || f.users.calls != 0 || f.db.groupReads != 0 || f.db.memberReads != 0 || f.callbacks.Load() != 0 || f.redis.reads != 0 || f.db.writes != 0 {
				t.Fatalf("unauthorized applicant crossed entry boundary: userLookups=%d groupReads=%d memberReads=%d callbacks=%d historyReads=%d writes=%d error=%v", f.users.calls, f.db.groupReads, f.db.memberReads, f.callbacks.Load(), f.redis.reads, f.db.writes, err)
			}
		})
	}
}

func TestJoinGroupPreservesSelfAndTrustedManagerAdmission(t *testing.T) {
	cases := []struct{ name, actor, applicant string }{
		{"ordinary-self", "synthetic-ordinary", "synthetic-ordinary"},
		{"configured-manager", "synthetic-join-manager", "synthetic-target"},
		{"configured-imadmin", "synthetic-join-imadmin", "synthetic-target"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			f := newJoinApplicantFixture(t)
			_, err := f.server.JoinGroup(joinApplicantContext(item.actor), &pbgroup.JoinGroupReq{GroupID: "synthetic-join-g", InviterUserID: item.applicant, JoinSource: constant.JoinBySearch})
			if !errors.Is(err, joinApplicantWriteAdmission) || f.users.calls != 1 || len(f.users.requested) != 1 || f.users.requested[0] != item.applicant || f.db.groupReads != 1 || f.db.memberReads != 1 || f.callbacks.Load() != 1 || f.redis.reads != 1 || f.db.writes != 1 || len(f.db.written) != 1 {
				t.Fatal("legitimate join did not reach its original writer", err)
			}
			written := f.db.written[0]
			if written.GroupID != "synthetic-join-g" || written.UserID != item.applicant || written.InviterUserID != item.applicant || written.OperatorUserID != item.actor || written.RoleLevel != constant.GroupOrdinaryUsers {
				t.Fatal("legacy applicant/operator/ordinary-role contract changed")
			}
		})
	}
}

func TestJoinGroupPreservesAuthorizedLookupFailure(t *testing.T) {
	f := newJoinApplicantFixture(t)
	f.users.failure = joinApplicantLookupFailure
	_, err := f.server.JoinGroup(joinApplicantContext("synthetic-ordinary"), &pbgroup.JoinGroupReq{GroupID: "synthetic-join-g", InviterUserID: "synthetic-ordinary"})
	if !errors.Is(err, joinApplicantLookupFailure) || f.users.calls != 1 || f.db.groupReads != 0 || f.db.memberReads != 0 || f.callbacks.Load() != 0 || f.redis.reads != 0 || f.db.writes != 0 {
		t.Fatal("authorized lookup failure continued into group mutation", err)
	}
}

// After-only robustness; excluded from shared before baseline because the old
// method dereferenced nil req. A panic must never count as permission RED.
func TestJoinGroupRejectsNilRequestBeforeDependencies(t *testing.T) {
	f := newJoinApplicantFixture(t)
	_, err := f.server.JoinGroup(joinApplicantContext("synthetic-ordinary"), nil)
	if !errs.ErrNoPermission.Is(err) || f.users.calls != 0 || f.db.groupReads != 0 || f.db.memberReads != 0 || f.callbacks.Load() != 0 || f.redis.reads != 0 || f.db.writes != 0 {
		t.Fatal("nil request reached a dependency", err)
	}
}
