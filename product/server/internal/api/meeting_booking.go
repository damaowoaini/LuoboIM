package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/meetingbooking"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"go.mongodb.org/mongo-driver/mongo"
)

type meetingBookingAPI struct {
	service   *meetingbooking.Service
	principal func(*gin.Context) (meetingbooking.Principal, error)
}
type bookingInvitePolicy struct {
	check func(context.Context, meetingbooking.Principal, []string) error
}

func (a bookingInvitePolicy) Check(ctx context.Context, p meetingbooking.Principal, ids []string) error {
	return a.check(ctx, p, ids)
}

func bookingPrincipal(c *gin.Context) (meetingbooking.Principal, error) {
	// authenticate middleware has already checked exact live token status. The
	// independent purpose check prevents delegated/service identities here.
	claims, err := authverify.ParseClassified(c.GetHeader(constant.Token))
	if err != nil || claims.Purpose != "" || claims.UserID == "" || claims.UserID != c.GetString(constant.OpUserID) {
		return meetingbooking.Principal{}, meetingbooking.ErrDenied
	}
	// Deployment issuer is a trusted tenant namespace, never a browser header,
	// request host, userID/platform parameter or unverified JWT payload.
	tenant := config.Config.AdminDelegation.Issuer
	if tenant == "" {
		return meetingbooking.Principal{}, errors.New("booking tenant namespace unconfigured")
	}
	return meetingbooking.Principal{Tenant: tenant, Owner: claims.UserID}, nil
}
func registerMeetingBookingRoutes(r gin.IRouter, authenticate gin.HandlerFunc, db *mongo.Database, discov discoveryregistry.SvcDiscoveryRegistry) {
	registerMeetingBookingRoutesWithContext(context.Background(), r, authenticate, db, discov)
}
func registerMeetingBookingRoutesWithContext(parent context.Context, r gin.IRouter, authenticate gin.HandlerFunc, db *mongo.Database, discov discoveryregistry.SvcDiscoveryRegistry) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	store, err := meetingbooking.NewMongoStore(ctx, db)
	if err != nil {
		// Keep every booking path registered with the same safe wire error, without
		// aborting construction of unrelated API routes. No DB repair is attempted.
		(&meetingBookingAPI{principal: bookingPrincipal}).routes(r, authenticate)
		return
	}
	users := rpcclient.NewUserRpcClient(discov)
	friends := rpcclient.NewFriendRpcClient(discov)
	policy := bookingInvitePolicy{check: func(ctx context.Context, p meetingbooking.Principal, ids []string) error {
		host, err := users.GetUserInfo(ctx, p.Owner)
		if err != nil || host == nil || host.UserID != p.Owner {
			return meetingbooking.ErrDenied
		}
		if len(ids) > 0 {
			rows, err := users.GetUsersInfo(ctx, ids)
			if err != nil || len(rows) != len(ids) {
				return meetingbooking.ErrDenied
			}
			found := map[string]bool{}
			for _, row := range rows {
				if row == nil {
					return meetingbooking.ErrDenied
				}
				found[row.UserID] = true
			}
			for _, id := range ids {
				if !found[id] {
					return meetingbooking.ErrDenied
				}
			}
		}
		for _, id := range ids {
			blocked, err := friends.IsBlocked(ctx, p.Owner, id)
			if err != nil || blocked {
				return meetingbooking.ErrDenied
			}
			if config.Config.MessageVerify.FriendVerify != nil && *config.Config.MessageVerify.FriendVerify {
				ok, err := friends.IsFriend(ctx, p.Owner, id)
				if err != nil || !ok {
					return meetingbooking.ErrDenied
				}
			}
		}
		return nil
	}}
	api := &meetingBookingAPI{service: &meetingbooking.Service{Store: store, Access: policy}, principal: bookingPrincipal}
	api.routes(r, authenticate)
}
func (a *meetingBookingAPI) routes(r gin.IRouter, authenticate gin.HandlerFunc) {
	group := r.Group("/signal/meeting_booking", authenticate)
	for _, operation := range []string{"create", "list", "detail", "update", "cancel", "receipt", "resolve"} {
		group.POST("/"+operation, a.handle(operation))
	}
}

var bookingPayloadKeys = []string{"meetingName", "startTime", "meetingDuration", "inviteeUserIDList", "ex"}

func bookingDecode(c *gin.Context, keys []string, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32768)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil || !utf8.Valid(data) {
		return meetingbooking.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return meetingbooking.ErrInvalid
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	seen := map[string]bool{}
	for decoder.More() {
		word, err := decoder.Token()
		if err != nil {
			return meetingbooking.ErrInvalid
		}
		key, ok := word.(string)
		if !ok || !allowed[key] || seen[key] {
			return meetingbooking.ErrInvalid
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return meetingbooking.ErrInvalid
		}
	}
	if _, err := decoder.Token(); err != nil {
		return meetingbooking.ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return meetingbooking.ErrInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return meetingbooking.ErrInvalid
	}
	return nil
}
func bookingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, meetingbooking.ErrInvalid):
		apiresp.GinError(c, errs.ErrArgs.Wrap("预约参数无效"))
	case errors.Is(err, meetingbooking.ErrConflict):
		apiresp.GinError(c, errs.ErrArgs.Wrap("预约意图或版本冲突，请查询原意图回执"))
	case errors.Is(err, meetingbooking.ErrDenied), errors.Is(err, meetingbooking.ErrNotFound):
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("无权访问此预约或记录不存在"))
	default:
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("预约结果未确认，请查询同一意图回执"))
	}
}
func (a *meetingBookingAPI) handle(operation string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := a.principal(c)
		if err != nil {
			bookingError(c, err)
			return
		}
		if a.service == nil || a.service.Store == nil {
			bookingError(c, meetingbooking.ErrUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, constant.OpUserID, principal.Owner)
		ctx = context.WithValue(ctx, constant.OpUserPlatform, c.GetString(constant.OpUserPlatform))
		if operationID := c.Value(constant.OperationID); operationID != nil {
			ctx = context.WithValue(ctx, constant.OperationID, operationID)
		}
		var response any
		switch operation {
		case "create":
			var req meetingbooking.CreateRequest
			err = bookingDecode(c, append([]string{"clientRequestID"}, bookingPayloadKeys...), &req)
			if err == nil {
				response, err = a.service.Create(ctx, principal, req)
			}
		case "update":
			var req meetingbooking.UpdateRequest
			err = bookingDecode(c, append([]string{"clientRequestID", "bookingID", "expectedVersion"}, bookingPayloadKeys...), &req)
			if err == nil {
				response, err = a.service.Update(ctx, principal, req)
			}
		case "cancel":
			var req meetingbooking.CancelRequest
			err = bookingDecode(c, []string{"clientRequestID", "bookingID", "expectedVersion"}, &req)
			if err == nil {
				response, err = a.service.Cancel(ctx, principal, req)
			}
		case "receipt", "resolve":
			var req meetingbooking.IntentRequest
			err = bookingDecode(c, []string{"clientRequestID"}, &req)
			if err == nil {
				if operation == "receipt" {
					response, err = a.service.Receipt(ctx, principal, req.ClientRequestID)
				} else {
					response, err = a.service.Resolve(ctx, principal, req.ClientRequestID)
				}
			}
		case "detail":
			var req meetingbooking.DetailRequest
			err = bookingDecode(c, []string{"bookingID"}, &req)
			if err == nil {
				response, err = a.service.Detail(ctx, principal, req.BookingID)
			}
		case "list":
			var req meetingbooking.ListRequest
			err = bookingDecode(c, []string{"pageSize", "cursor", "includeCancelled"}, &req)
			if err == nil {
				response, err = a.service.List(ctx, principal, req)
			}
		default:
			err = meetingbooking.ErrInvalid
		}
		if err != nil {
			bookingError(c, err)
			return
		}
		apiresp.GinSuccess(c, response)
	}
}
