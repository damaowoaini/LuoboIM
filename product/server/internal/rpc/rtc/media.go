package rtc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type PublishPermission struct{ Microphone, Camera, Screen bool }
type Media interface {
	Token(string, string, string, PublishPermission) (string, error)
	DeleteRoom(context.Context, string) error
	Remove(context.Context, string, string) error
	Permissions(context.Context, string, string, PublishPermission) error
	Mute(context.Context, string, string, string, bool) error
}
type LiveKit struct {
	LiveURL string
	APIURL  string
	APIKey  string
	secret  []byte
	client  *http.Client
}

func NewLiveKitFromEnv() (*LiveKit, error) {
	live, err := url.Parse(os.Getenv("LUOBO_RTC_LIVE_URL"))
	if err != nil || live.Host == "" || live.User != nil || live.RawQuery != "" || live.Fragment != "" || live.Path != "" && live.Path != "/" {
		return nil, fmt.Errorf("invalid RTC live URL")
	}
	host := live.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || ip != nil && ip.IsLoopback()
	if live.Scheme != "wss" && (live.Scheme != "ws" || !loopback || os.Getenv("LUOBO_RTC_ALLOW_LOOPBACK") != "true") {
		return nil, fmt.Errorf("RTC live URL must use WSS")
	}
	key, secret := os.Getenv("LUOBO_RTC_API_KEY"), os.Getenv("LUOBO_RTC_API_SECRET")
	if key == "" || len(secret) < 32 {
		return nil, fmt.Errorf("RTC media credentials are not configured")
	}
	api := *live
	if api.Scheme == "wss" {
		api.Scheme = "https"
	} else {
		api.Scheme = "http"
	}
	api.Path = ""
	return &LiveKit{LiveURL: strings.TrimSuffix(live.String(), "/"), APIURL: api.String(), APIKey: key, secret: []byte(secret), client: &http.Client{Timeout: 10 * time.Second}}, nil
}
func sources(permission PublishPermission) []string {
	out := []string{}
	if permission.Microphone {
		out = append(out, "microphone")
	}
	if permission.Camera {
		out = append(out, "camera")
	}
	if permission.Screen {
		out = append(out, "screen_share", "screen_share_audio")
	}
	return out
}
func (l *LiveKit) Token(room, actor, metadata string, permission PublishPermission) (string, error) {
	now := time.Now().UTC()
	grants := map[string]any{"roomJoin": true, "room": room, "canSubscribe": true, "canPublish": len(sources(permission)) > 0, "canPublishData": true, "canPublishSources": sources(permission), "canUpdateOwnMetadata": false}
	claims := jwt.MapClaims{"iss": l.APIKey, "sub": actor, "iat": now.Unix(), "nbf": now.Add(-5 * time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "video": grants, "metadata": metadata}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(l.secret)
}
func (l *LiveKit) rpc(ctx context.Context, method, room string, body any, result any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": l.APIKey, "iat": now.Unix(), "nbf": now.Add(-5 * time.Second).Unix(), "exp": now.Add(time.Minute).Unix(), "video": map[string]any{"room": room, "roomAdmin": true, "roomCreate": true, "roomList": true}}).SignedString(l.secret)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", l.APIURL+"/twirp/livekit.RoomService/"+method, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := l.client.Do(request)
	if err != nil {
		return fmt.Errorf("RTC media service unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return fmt.Errorf("invalid RTC media response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Code string `json:"code"`
		}
		json.Unmarshal(data, &failure)
		if failure.Code == "not_found" && (method == "DeleteRoom" || method == "UpdateParticipant" || method == "MutePublishedTrack" || method == "RemoveParticipant") {
			return nil
		}
		return fmt.Errorf("RTC media operation %s failed (%d)", method, response.StatusCode)
	}
	if result != nil && len(data) > 0 {
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("invalid RTC media response")
		}
	}
	return nil
}
func (l *LiveKit) DeleteRoom(ctx context.Context, room string) error {
	return l.rpc(ctx, "DeleteRoom", room, map[string]any{"room": room}, nil)
}
func (l *LiveKit) Permissions(ctx context.Context, room, actor string, permission PublishPermission) error {
	enums := []string{}
	for _, source := range sources(permission) {
		enums = append(enums, strings.ToUpper(source))
	}
	return l.rpc(ctx, "UpdateParticipant", room, map[string]any{"room": room, "identity": actor, "permission": map[string]any{"can_publish": len(enums) > 0, "can_subscribe": true, "can_publish_data": true, "can_publish_sources": enums, "can_update_metadata": false}}, nil)
}
func (l *LiveKit) Mute(ctx context.Context, room, actor, stream string, mute bool) error {
	var response struct {
		Participants []struct {
			Identity string `json:"identity"`
			Tracks   []struct {
				SID    string `json:"sid"`
				Source any    `json:"source"`
			} `json:"tracks"`
		} `json:"participants"`
	}
	if err := l.rpc(ctx, "ListParticipants", room, map[string]any{"room": room}, &response); err != nil {
		return err
	}
	for _, participant := range response.Participants {
		if participant.Identity != actor {
			continue
		}
		for _, track := range participant.Tracks {
			raw := strings.ToUpper(fmt.Sprint(track.Source))
			matches := stream == "audio" && (raw == "MICROPHONE" || raw == "2") || stream == "video" && (raw == "CAMERA" || raw == "1") || stream == "screen" && (raw == "SCREEN_SHARE" || raw == "3" || raw == "SCREEN_SHARE_AUDIO" || raw == "4")
			if matches {
				if err := l.rpc(ctx, "MutePublishedTrack", room, map[string]any{"room": room, "identity": actor, "track_sid": track.SID, "muted": mute}, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (l *LiveKit) VerifyWebhook(body []byte, authorization string) error {
	authorization = strings.TrimPrefix(authorization, "Bearer ")
	token, err := jwt.Parse(authorization, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("invalid webhook algorithm")
		}
		return l.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		return fmt.Errorf("invalid RTC webhook credential")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["iss"] != l.APIKey || !claims.VerifyExpiresAt(time.Now().Unix(), true) {
		return fmt.Errorf("invalid RTC webhook issuer")
	}
	expected, err := base64.StdEncoding.DecodeString(fmt.Sprint(claims["sha256"]))
	digest := sha256.Sum256(body)
	if err != nil || !hmac.Equal(expected, digest[:]) {
		return fmt.Errorf("RTC webhook body mismatch")
	}
	return nil
}

func (l *LiveKit) Remove(ctx context.Context, room, actor string) error {
	return l.rpc(ctx, "RemoveParticipant", room, map[string]any{"room": room, "identity": actor}, nil)
}
