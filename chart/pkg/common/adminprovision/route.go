package adminprovision

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const CommitPath = "/api/internal/admin_provisioning/commit"
const ReceiptPath = "/api/internal/admin_provisioning/receipt"

type RouteResult struct {
	Found        bool   `json:"found"`
	ReceiptID    string `json:"receiptID"`
	ServerID     string `json:"serverID"`
	UserID       string `json:"userID"`
	BindingState string `json:"bindingState"`
	Retryable    bool   `json:"retryable"`
	Reason       string `json:"reason"`
}
type Route interface {
	Query(context.Context, string) (*RouteResult, error)
	Commit(context.Context, []byte) (*RouteResult, error)
}
type RouteClient struct {
	ServerID          string
	Secret            []byte
	Base              string
	AllowLoopbackHTTP bool
	Client            *http.Client
}

func (c *RouteClient) Validate() error {
	u, e := url.Parse(c.Base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.TrimSpace(c.ServerID) != c.ServerID || c.ServerID == "" || len(c.ServerID) > 64 || len(c.Secret) < 32 {
		return errors.New("invalid provisioning upstream configuration")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !c.AllowLoopbackHTTP || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return errors.New("provisioning upstream requires HTTPS")
		}
	}
	return nil
}
func Signature(secret []byte, serverID, path, timestamp, nonce string, body []byte) string {
	derived := hmac.New(sha256.New, secret)
	derived.Write([]byte("luobo-admin-provisioning/v1"))
	mac := hmac.New(sha256.New, derived.Sum(nil))
	mac.Write([]byte(timestamp + "\n" + nonce + "\nPOST\n" + path + "\n" + serverID + "\n" + Hash(body)))
	return hex.EncodeToString(mac.Sum(nil))
}
func (c *RouteClient) call(ctx context.Context, path string, body []byte) (*RouteResult, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	ts, n := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(nonce)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.Base, "/")+path, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("operationID", "admin-provision-"+n)
	req.Header.Set("X-Luobo-Route-Server-ID", c.ServerID)
	req.Header.Set("X-Luobo-Route-Timestamp", ts)
	req.Header.Set("X-Luobo-Route-Nonce", n)
	req.Header.Set("X-Luobo-Route-Signature", Signature(c.Secret, c.ServerID, path, ts, n, body))
	client := http.Client{Timeout: 3 * time.Second}
	if c.Client != nil {
		client = *c.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("provisioning redirect rejected") }
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("provisioning upstream unavailable")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(data) > 65536 {
		return nil, errors.New("invalid provisioning response")
	}
	var envelope struct {
		ErrCode int          `json:"errCode"`
		Data    *RouteResult `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &envelope) != nil || envelope.ErrCode != 0 || envelope.Data == nil {
		return nil, fmt.Errorf("provisioning upstream rejected(status=%d,code=%d)", resp.StatusCode, envelope.ErrCode)
	}
	return envelope.Data, nil
}
func (c *RouteClient) Query(ctx context.Context, id string) (*RouteResult, error) {
	if !ValidID(id) {
		return nil, errors.New("invalid receipt")
	}
	data, _ := json.Marshal(struct {
		ReceiptID string `json:"receiptID"`
	}{id})
	return c.call(ctx, ReceiptPath, data)
}
func (c *RouteClient) Commit(ctx context.Context, body []byte) (*RouteResult, error) {
	return c.call(ctx, CommitPath, body)
}
