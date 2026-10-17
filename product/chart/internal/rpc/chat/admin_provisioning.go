package chat

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	chatClient "github.com/OpenIMSDK/chat/pkg/rpclient/chat"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"gorm.io/gorm"
)

type provisionIM struct {
	caller *apicall.Caller
	admin  *chatClient.AdminClient
}

func (p *provisionIM) authorized(ctx context.Context) (context.Context, error) {
	ctx = mctx.WithAdminUser(ctx)
	ctx = context.WithValue(ctx, constant.OperationID, "admin-provisioning")
	token, e := p.caller.ImAdminTokenWithDefaultAdmin(ctx)
	if e != nil {
		return nil, e
	}
	return mctx.WithApiToken(ctx, token), nil
}
func (p *provisionIM) Find(ctx context.Context, id string) (*adminprovision.User, error) {
	ctx, e := p.authorized(ctx)
	if e != nil {
		return nil, e
	}
	users, e := p.caller.FindRegisteredUsers(ctx, []string{id})
	if e != nil {
		if errs.ErrRecordNotFound.Is(e) {
			return nil, nil
		}
		return nil, e
	}
	if len(users) == 0 {
		return nil, nil
	}
	if len(users) != 1 || users[0].UserID != id {
		return nil, errors.New("ambiguous IM owner")
	}
	marker, purpose, e := adminprovision.ReadIMMarker(users[0].Ex)
	if e != nil {
		return nil, e
	}
	return &adminprovision.User{ID: id, Nickname: users[0].Nickname, FaceURL: users[0].FaceURL, ProvisioningID: marker, ProvisioningPurpose: purpose, CreateTime: users[0].CreateTime}, nil
}
func (p *provisionIM) Create(ctx context.Context, u adminprovision.User) error {
	ctx, e := p.authorized(ctx)
	if e != nil {
		return e
	}
	key := "luoboAdminProvisioningID"
	if u.ProvisioningPurpose == adminprovision.PublicPurpose {
		key = "luoboRegistrationID"
	} else if u.ProvisioningPurpose != adminprovision.AdminPurpose {
		return errors.New("unknown IM ownership purpose")
	}
	marker, _ := json.Marshal(map[string]string{key: u.ProvisioningID})
	return p.caller.RegisterUser(ctx, []*sdkws.UserInfo{{UserID: u.ID, Nickname: u.Nickname, FaceURL: u.FaceURL, Ex: string(marker), CreateTime: u.CreateTime}})
}
func (p *provisionIM) PlanDefaults(ctx context.Context, all bool) (adminprovision.Defaults, error) {
	ctx = mctx.WithAdminUser(ctx)
	ctx = context.WithValue(ctx, constant.OperationID, "admin-provision-defaults")
	friends, e := p.admin.GetDefaultFriendUserID(ctx)
	if e != nil {
		return adminprovision.Defaults{}, e
	}
	groups, e := p.admin.GetDefaultGroupID(ctx)
	if e != nil {
		return adminprovision.Defaults{}, e
	}
	// Keep the complete configured plan for existing import workflows. Delivery
	// is bounded by the worker context and resumes through idempotent checks.
	if !all && len(friends) > 1 {
		n, e := rand.Int(rand.Reader, big.NewInt(int64(len(friends))))
		if e != nil {
			return adminprovision.Defaults{}, e
		}
		friends = []string{friends[n.Int64()]}
	}
	return adminprovision.Defaults{Friends: friends, Groups: groups}, nil
}
func (p *provisionIM) ApplyDefaults(ctx context.Context, id string, plan adminprovision.Defaults) error {
	ctx, e := p.authorized(ctx)
	if e != nil {
		return e
	}
	contains := func(ids []string, id string) bool {
		for _, v := range ids {
			if v == id {
				return true
			}
		}
		return false
	}
	if len(plan.Friends) > 0 {
		existing, e := p.caller.FriendUserIDs(ctx, id)
		if e != nil {
			return e
		}
		pending := []string{}
		for _, f := range plan.Friends {
			if f != id && !contains(existing, f) {
				pending = append(pending, f)
			}
		}
		if len(pending) > 0 {
			if e = p.caller.ImportFriend(ctx, id, pending); e != nil {
				return e
			}
			existing, e = p.caller.FriendUserIDs(ctx, id)
			if e != nil {
				return e
			}
			for _, f := range pending {
				if !contains(existing, f) {
					return errors.New("default friendship not confirmed")
				}
			}
		}
	}
	for _, g := range plan.Groups {
		members, e := p.caller.FindGroupMemberUserIDs(ctx, g)
		if e != nil {
			return e
		}
		if contains(members, id) {
			continue
		}
		if e = p.caller.InviteToGroupStrict(ctx, id, g); e != nil {
			return e
		}
		members, e = p.caller.FindGroupMemberUserIDs(ctx, g)
		if e != nil || !contains(members, id) {
			return errors.New("default group membership not confirmed")
		}
	}
	return nil
}
func newAdminProvisioning(db *gorm.DB, admin *chatClient.AdminClient) (*adminprovision.Service, error) {
	cfg := config.Config.AdminProvisioning
	service := &adminprovision.Service{DB: db, ServerID: cfg.ServerID, RouteEnabled: cfg.Enabled, IM: &provisionIM{caller: &apicall.Caller{}, admin: admin}}
	if cfg.Enabled {
		route := &adminprovision.RouteClient{ServerID: cfg.ServerID, Base: cfg.RouteURL, Secret: []byte(os.Getenv("LUOBO_ROUTE_SHARED_SECRET")), AllowLoopbackHTTP: cfg.AllowLoopbackHTTP}
		if e := route.Validate(); e != nil {
			return nil, e
		}
		service.Route = route
	}
	return service, nil
}

// The API may stop waiting while the SQL outbox continues on the worker.
func processAdminProvisioning(s *adminprovision.Service, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_ = s.Process(ctx, id)
}
