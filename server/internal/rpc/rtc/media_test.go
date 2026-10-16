package rtc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func fixtureLiveKit(t *testing.T) *LiveKit {
	t.Helper()
	path := os.Getenv("LUOBO_INTEGRATION_LIVEKIT_CREDENTIALS")
	if path == "" {
		t.Skip("requires private LiveKit fixture")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("fixture credentials must be private")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("fixture credentials unavailable")
	}
	var cfg struct {
		Key    string `json:"apiKey"`
		Secret string `json:"apiSecret"`
		URL    string `json:"wsURL"`
	}
	if json.Unmarshal(data, &cfg) != nil || cfg.URL != "ws://127.0.0.1:37880" {
		t.Fatal("only task-private fixture permitted")
	}
	t.Setenv("LUOBO_RTC_LIVE_URL", cfg.URL)
	t.Setenv("LUOBO_RTC_API_KEY", cfg.Key)
	t.Setenv("LUOBO_RTC_API_SECRET", cfg.Secret)
	t.Setenv("LUOBO_RTC_ALLOW_LOOPBACK", "true")
	media, err := NewLiveKitFromEnv()
	if err != nil {
		t.Fatal("fixture setup failed")
	}
	return media
}
func TestLiveKitConfigurationAndWebhookAuthentication(t *testing.T) {
	t.Setenv("LUOBO_RTC_API_KEY", "test-key")
	t.Setenv("LUOBO_RTC_API_SECRET", strings.Repeat("z", 32))
	t.Setenv("LUOBO_RTC_ALLOW_LOOPBACK", "")
	for _, url := range []string{"ws://rtc.example.com", "ws://127.0.0.1:37880", "wss://user:password@rtc.example.com", "wss://rtc.example.com?secret=value", "wss://rtc.example.com/path"} {
		t.Setenv("LUOBO_RTC_LIVE_URL", url)
		if _, err := NewLiveKitFromEnv(); err == nil {
			t.Fatal("insecure media configuration accepted")
		}
	}
	t.Setenv("LUOBO_RTC_LIVE_URL", "wss://rtc.example.com")
	media, err := NewLiveKitFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"evt","event":"room_started"}`)
	digest := sha256.Sum256(body)
	claims := jwt.MapClaims{"iss": media.APIKey, "exp": time.Now().Add(time.Minute).Unix(), "sha256": base64.StdEncoding.EncodeToString(digest[:])}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(media.secret)
	if media.VerifyWebhook(body, token) != nil || media.VerifyWebhook(body, "Bearer "+token) != nil {
		t.Fatal("valid LiveKit webhook rejected")
	}
	if media.VerifyWebhook(append(body, ' '), token) == nil {
		t.Fatal("tampered webhook accepted")
	}
	claims["iss"] = "wrong"
	wrong, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(media.secret)
	if media.VerifyWebhook(body, wrong) == nil {
		t.Fatal("wrong issuer accepted")
	}
	claims["iss"] = media.APIKey
	claims["exp"] = time.Now().Add(-time.Minute).Unix()
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(media.secret)
	if media.VerifyWebhook(body, expired) == nil {
		t.Fatal("expired webhook accepted")
	}
	delete(claims, "exp")
	withoutExpiry, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(media.secret)
	if media.VerifyWebhook(body, withoutExpiry) == nil {
		t.Fatal("nonexpiring webhook accepted")
	}
}
func TestLiveKitRealJWTWebSocketAndScopedRoomManagement(t *testing.T) {
	media := fixtureLiveKit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	room := "luobo-go-" + uuid.NewString()
	id := "luobo-test-participant"
	token, err := media.Token(room, id, `{"verified":true}`, PublishPermission{Microphone: true})
	if err != nil {
		t.Fatal("mint failed")
	}
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return media.secret, nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		t.Fatal("minted JWT invalid")
	}
	claims := parsed.Claims.(jwt.MapClaims)
	video := claims["video"].(map[string]any)
	if claims["sub"] != id || video["room"] != room || video["roomJoin"] != true || video["roomAdmin"] != nil || claims["exp"].(float64)-claims["iat"].(float64) > 120 {
		t.Fatal("participant JWT grants invalid")
	}
	t.Cleanup(func() { media.DeleteRoom(context.Background(), room) })
	connection, response, err := websocket.DefaultDialer.DialContext(ctx, media.LiveURL+"/rtc?protocol=15&sdk=go&auto_subscribe=1", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("real LiveKit signaling authentication failed (HTTP %d)", status)
	}
	defer connection.Close()
	connection.SetReadDeadline(time.Now().Add(8 * time.Second))
	if _, payload, err := connection.ReadMessage(); err != nil || len(payload) == 0 {
		t.Fatal("real LiveKit join response missing")
	}
	var result struct {
		Participants []struct {
			Identity, Metadata string
			Permission         struct {
				CanPublish bool `json:"can_publish"`
			}
		} `json:"participants"`
	}
	if err := media.rpc(ctx, "ListParticipants", room, map[string]any{"room": room}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Participants) != 1 || result.Participants[0].Identity != id || result.Participants[0].Metadata != `{"verified":true}` {
		t.Fatal("server did not receive scoped identity and metadata")
	}
	// A participant credential must not gain room administration rights.
	payload, _ := json.Marshal(map[string]any{"room": room})
	request, _ := http.NewRequestWithContext(ctx, "POST", media.APIURL+"/twirp/livekit.RoomService/DeleteRoom", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	denied, err := media.client.Do(request)
	if err != nil {
		t.Fatal("room privilege test unavailable")
	}
	io.Copy(io.Discard, denied.Body)
	denied.Body.Close()
	if denied.StatusCode < 400 {
		t.Fatal("join token gained room deletion privileges")
	}
	if err := media.Permissions(ctx, room, id, PublishPermission{}); err != nil {
		t.Fatal("real permission revoke failed:", err)
	}
	if err := media.Mute(ctx, room, id, "audio", true); err != nil {
		t.Fatal("real mute query failed:", err)
	}
	if err := media.Remove(ctx, room, id); err != nil {
		t.Fatal("real participant remove failed:", err)
	}
	if err := media.DeleteRoom(ctx, room); err != nil {
		t.Fatal("real room cleanup failed:", err)
	}
	t.Log("LUOBO_LIVEKIT_REAL_AUTHORIZATION_PASS")
}
