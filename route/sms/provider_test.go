package sms

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func TestSMSProviderErrorsNeverExposeCredentialURLs(t *testing.T) {
	provider := NewAccessyouSMS(AccessyouConfig{Username: "u", Password: "do-not-leak", AccountNo: "account", TemplateId: "otp"})
	provider.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("GET https://sms.example?pwd=do-not-leak&a=123456")
	})
	err := provider.SendCode("+852", "12345678", "123456")
	if err == nil || strings.Contains(err.Error(), "do-not-leak") || strings.Contains(err.Error(), "123456") {
		t.Fatal("provider leaked sensitive URL")
	}
}
func TestAccessyouInternationalPhoneAndBOMResponse(t *testing.T) {
	provider := NewAccessyouSMS(AccessyouConfig{Username: "u", Password: "secret", AccountNo: "account", TemplateId: "otp"})
	provider.client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("phone") != "85212345678" {
			t.Fatal("international phone incorrectly formatted")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("\ufeff" + `{"msg_status":"100"}`))}, nil
	})
	if err := provider.SendCode("+852", "12345678", "123456"); err != nil {
		t.Fatal(err)
	}
}
func TestProviderRejectsHTTPAndDisabledProviderFails(t *testing.T) {
	provider := NewSmsbaoSMS(SmsbaoConfig{ApiUrl: "http://sms.example", Username: "u", Password: "p", SignName: "萝卜"})
	if provider.SendCode("+86", "13812345678", "123456") == nil {
		t.Fatal("plaintext SMS request allowed")
	}
	disabled, _ := New(Config{})
	if disabled.SendCode("+86", "13812345678", "123456") == nil {
		t.Fatal("disabled provider reported false success")
	}
	if provider.SendCode("+852", "12345678", "123456") == nil {
		t.Fatal("domestic provider accepted wrong country")
	}
}
