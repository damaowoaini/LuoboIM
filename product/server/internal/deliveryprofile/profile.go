// Package deliveryprofile defines non-secret, immutable transport configuration.
// It carries no recipient/member write authority and performs no network/DDL.
package deliveryprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid owned delivery profile")

func SHA(body []byte) string { s := sha256.Sum256(body); return hex.EncodeToString(s[:]) }
func Digest(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return SHA(body)
}
func validSHA(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}

// Transport deliberately retains the old JSON/BSON order and tags. Version is
// an explicit source type, never an invented Kafka topic/partition/offset.
type Transport struct {
	Version                string `bson:"version" json:"version"`
	Epoch                  string `bson:"epoch" json:"epoch"`
	ActivationSHA256       string `bson:"activation_sha256" json:"activationSHA256"`
	ConfigurationSHA256    string `bson:"configuration_sha256" json:"configurationSHA256"`
	Discovery              string `bson:"discovery" json:"discovery"`
	BeforeGroupPushBinding string `bson:"before_group_push_binding" json:"beforeGroupPushBinding"`
	BeforeGroupFailureMode string `bson:"before_group_failure_mode" json:"beforeGroupFailureMode"`
	OnlinePushBinding      string `bson:"online_push_binding" json:"onlinePushBinding"`
	OnlineFailureMode      string `bson:"online_failure_mode" json:"onlineFailureMode"`
	OfflinePushBinding     string `bson:"offline_push_binding" json:"offlinePushBinding"`
	OfflineFailureMode     string `bson:"offline_failure_mode" json:"offlineFailureMode"`
	Vendor                 string `bson:"vendor" json:"vendor"`
	VendorBinding          string `bson:"vendor_binding" json:"vendorBinding"`
	EncryptionRequired     bool   `bson:"encryption_required" json:"encryptionRequired"`
}

func (p Transport) Fingerprint() (string, error) {
	if p.Version != "ordinary-kafka-v1" && p.Version != "group-effect-v1" || !validID(p.Epoch) || !validSHA(p.ActivationSHA256) || !validSHA(p.ConfigurationSHA256) || p.Discovery != "zookeeper" && p.Discovery != "k8s" {
		return "", ErrInvalid
	}
	for _, v := range []struct{ binding, mode string }{{p.BeforeGroupPushBinding, p.BeforeGroupFailureMode}, {p.OnlinePushBinding, p.OnlineFailureMode}, {p.OfflinePushBinding, p.OfflineFailureMode}} {
		if v.binding == "" {
			if v.mode != "" {
				return "", ErrInvalid
			}
			continue
		}
		if !validSHA(v.binding) || v.mode != "default-stop" && v.mode != "explicit-stop" && v.mode != "continue" {
			return "", ErrInvalid
		}
	}
	switch p.Vendor {
	case "fcm", "getui", "jpush":
		if !validSHA(p.VendorBinding) {
			return "", ErrInvalid
		}
	case "none":
		if p.VendorBinding != "" {
			return "", ErrInvalid
		}
	default:
		return "", ErrInvalid
	}
	return Digest(p), nil
}

type Callback struct {
	Binding string `bson:"binding" json:"binding"`
	Mode    string `bson:"mode" json:"mode"`
	// Timeout zero/negative is the original15s default, never an endpoint bypass.
	TimeoutSeconds int `bson:"timeout_seconds" json:"timeoutSeconds"`
}

func (c Callback) Valid() bool {
	if c.Binding == "" {
		return c.Mode == "" && c.TimeoutSeconds == 0
	}
	return validSHA(c.Binding) && (c.Mode == "default-stop" || c.Mode == "explicit-stop" || c.Mode == "continue")
}

// Group freezes the complete receiver obligations BEFORE the business commit.
// Enabled callbacks/vendors may not be dropped by a convenient runtime config.
type Group struct {
	Version       string    `bson:"version" json:"version"`
	Transport     Transport `bson:"transport" json:"transport"`
	BeforeMessage Callback  `bson:"before_message" json:"beforeMessage"`
	ModifyMessage Callback  `bson:"modify_message" json:"modifyMessage"`
	AfterMessage  Callback  `bson:"after_message" json:"afterMessage"`
	RiskAction    string    `bson:"risk_action" json:"riskAction"`
}

func (p Group) Fingerprint() (string, error) {
	if p.Version != "owned-group-effect-v1" || p.Transport.Version != "group-effect-v1" || !p.BeforeMessage.Valid() || !p.ModifyMessage.Valid() || !p.AfterMessage.Valid() || p.RiskAction != "group-message" {
		return "", ErrInvalid
	}
	if _, err := p.Transport.Fingerprint(); err != nil {
		return "", err
	}
	return Digest(p), nil
}
