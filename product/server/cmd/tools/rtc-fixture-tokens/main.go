// rtc-fixture-tokens creates short-lived tokens exclusively for a local test
// LiveKit instance. It never prints credentials or tokens.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/rpc/rtc"
)

func main() {
	credentials := flag.String("credentials", "", "private localhost fixture credential JSON")
	output := flag.String("output", "", "new private output file (must not exist)")
	flag.Parse()
	if err := run(*credentials, *output); err != nil {
		fmt.Fprintln(os.Stderr, "RTC fixture token generation failed:", err)
		os.Exit(1)
	}
}
func run(credentials, output string) error {
	info, err := os.Stat(credentials)
	if err != nil {
		return fmt.Errorf("credential file unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("credential file must be private")
	}
	data, err := os.ReadFile(credentials)
	if err != nil {
		return fmt.Errorf("credential file unreadable")
	}
	var cfg struct {
		APIKey    string `json:"apiKey"`
		APISecret string `json:"apiSecret"`
		WSURL     string `json:"wsURL"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return fmt.Errorf("invalid credential file")
	}
	parsed, err := url.Parse(cfg.WSURL)
	if err != nil || parsed.Scheme != "ws" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("only isolated localhost WS fixtures are supported")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("fixture must use a loopback IP")
	}
	os.Setenv("LUOBO_RTC_LIVE_URL", cfg.WSURL)
	os.Setenv("LUOBO_RTC_API_KEY", cfg.APIKey)
	os.Setenv("LUOBO_RTC_API_SECRET", cfg.APISecret)
	os.Setenv("LUOBO_RTC_ALLOW_LOOPBACK", "true")
	media, err := rtc.NewLiveKitFromEnv()
	if err != nil {
		return err
	}
	type participant struct {
		UserID string `json:"userID"`
		Token  string `json:"token"`
	}
	result := struct {
		LiveURL      string        `json:"liveURL"`
		RoomID       string        `json:"roomID"`
		ExpiresAt    int64         `json:"expiresAt"`
		Participants []participant `json:"participants"`
	}{LiveURL: cfg.WSURL, RoomID: "luobo-media-" + uuid.NewString(), ExpiresAt: time.Now().Add(2 * time.Minute).Unix()}
	for _, id := range []string{"luobo-media-a", "luobo-media-b"} {
		token, err := media.Token(result.RoomID, id, `{"fixture":true}`, rtc.PublishPermission{Microphone: true, Camera: true})
		if err != nil {
			return fmt.Errorf("token signing failed")
		}
		result.Participants = append(result.Participants, participant{UserID: id, Token: token})
	}
	if output == "" {
		output = os.TempDir() + "/luobo-rtc-tokens-" + uuid.NewString() + ".json"
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot create private output file")
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		os.Remove(output)
		return fmt.Errorf("cannot persist fixture output")
	}
	fmt.Println(output)
	return nil
}
