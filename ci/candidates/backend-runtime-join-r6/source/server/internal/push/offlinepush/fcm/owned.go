package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"firebase.google.com/go"
	"firebase.google.com/go/messaging"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	http2 "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"net/http"
	"path/filepath"
)

var errOwnedFCM = errors.New("original owned FCM request is unconfirmed")

type ownedFCM struct {
	parent context.Context
	owner  *ownedsource.Observer
	cancel context.CancelFunc
}

// Explicit configured private service-account only. No ambient ADC/project or
// shared HTTP/OAuth transport is acquired. Parsing does not request a token.
func NewOwnedClient(parent context.Context, cache cache.MsgModel, httpOwner *http2.OwnedCallbackHTTP, cancelOwner context.CancelFunc) (*Fcm, error) {
	path := filepath.Join(config.GetProjectRoot(), "config", config.Config.Push.Fcm.ServiceAccount)
	return newOwnedClient(parent, cache, httpOwner, cancelOwner, path)
}
func newOwnedClient(parent context.Context, cache cache.MsgModel, httpOwner *http2.OwnedCallbackHTTP, cancelOwner context.CancelFunc, path string) (*Fcm, error) {
	owner := ownedsource.FromContext(parent)
	if parent == nil || parent.Done() == nil || owner == nil || httpOwner == nil || cancelOwner == nil || cache == nil {
		return nil, errOwnedFCM
	}
	raw, err := namespaceinventory.ReadPrivateDocument(path, 128<<10)
	if err != nil {
		return nil, errOwnedFCM
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	var identity struct {
		Type     string `json:"type"`
		Project  string `json:"project_id"`
		TokenURI string `json:"token_uri"`
	}
	if json.Unmarshal(raw, &identity) != nil || identity.Type != "service_account" || identity.Project == "" || identity.TokenURI != "https://oauth2.googleapis.com/token" {
		return nil, errOwnedFCM
	}
	authContext := context.WithValue(parent, oauth2.HTTPClient, httpOwner.HTTPClient())
	credentials, err := google.CredentialsFromJSONWithType(authContext, raw, google.ServiceAccount, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil || credentials.ProjectID != identity.Project {
		return nil, errOwnedFCM
	}
	authenticated := oauth2.NewClient(authContext, credentials.TokenSource)
	// Both token acquisition and the resource request use the same exact private
	// transport. Its uncertain first result cancels parent before SDK retry.
	authenticated.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	app, err := firebase.NewApp(parent, &firebase.Config{ProjectID: credentials.ProjectID}, option.WithHTTPClient(authenticated))
	if err != nil {
		return nil, errOwnedFCM
	}
	client, err := app.Messaging(parent)
	if err != nil {
		return nil, errOwnedFCM
	}
	return &Fcm{fcmMsgCli: client, cache: cache, owned: &ownedFCM{parent, owner, cancelOwner}}, nil
}
func (f *Fcm) pushOwned(caller context.Context, users []string, title, content string, opts *offlinepush.Opts) error {
	if caller == nil || caller.Err() != nil || f.owned.parent.Err() != nil || opts == nil {
		return errOwnedFCM
	}
	ctx, cancel := context.WithCancel(caller)
	defer cancel()
	stop := context.AfterFunc(f.owned.parent, cancel)
	defer stop()
	notification := &messaging.Notification{Title: title, Body: content}
	var messages []*messaging.Message
	flush := func() error {
		if len(messages) == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return errOwnedFCM
		}
		before := f.owned.owner.Snapshot().PushDelivery.Started
		response, err := f.fcmMsgCli.SendAll(ctx, messages)
		if err != nil || response == nil || len(response.Responses) != len(messages) || response.SuccessCount+response.FailureCount != len(messages) || response.FailureCount != 0 {
			if f.owned.owner.Snapshot().PushDelivery.Started != before {
				f.owned.owner.Attempt(ownedsource.PushDelivery)(true)
				f.owned.cancel()
			}
			return errOwnedFCM
		}
		messages = messages[:0]
		return nil
	}
	for _, user := range users {
		var tokens []string
		for _, platform := range Terminal {
			token, err := f.cache.GetFcmToken(ctx, user, platform)
			if err == nil {
				if token != "" {
					tokens = append(tokens, token)
				}
			} else if !errors.Is(err, redis.Nil) {
				return errOwnedFCM
			}
		}
		apns := &messaging.APNSConfig{Payload: &messaging.APNSPayload{Aps: &messaging.Aps{Sound: opts.IOSPushSound}}}
		if opts.IOSBadgeCount {
			badge, err := f.cache.IncrUserBadgeUnreadCountSum(ctx, user)
			if err != nil {
				return errOwnedFCM
			}
			apns.Payload.Aps.Badge = &badge
		} else {
			badge, err := f.cache.GetUserBadgeUnreadCountSum(ctx, user)
			if err != nil && !errors.Is(err, redis.Nil) {
				return errOwnedFCM
			}
			if badge == 0 {
				badge = 1
			}
			apns.Payload.Aps.Badge = &badge
		}
		for _, token := range tokens {
			messages = append(messages, &messaging.Message{Data: map[string]string{"ex": opts.Ex}, Token: token, Notification: notification, APNS: apns})
			if len(messages) == SinglePushCountLimit {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}
