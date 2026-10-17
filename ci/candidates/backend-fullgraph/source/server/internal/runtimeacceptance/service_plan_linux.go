//go:build linux

package runtimeacceptance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/msgtransfer"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"gopkg.in/yaml.v3"
)

// These are build selectors, not observations of a running process. Launcher
// independently reads the original build receipt and live /proc executable.
type BinaryArtifact struct {
	Role          string   `json:"role"`
	Path          string   `json:"path"`
	SHA256        string   `json:"sha256"`
	SourceSHA256  string   `json:"sourceSHA256"`
	ReceiptPath   string   `json:"receiptPath"`
	ReceiptSHA256 string   `json:"receiptSHA256"`
	Services      []string `json:"services"`
	Components    []string `json:"components"`
}
type ServicePlan struct {
	Specs                []ownedcontrol.ServiceSpec
	Requirements         []ownedcontrol.NamespaceRuntimeRequirement
	ReaderPorts          map[string]int
	ReaderDiscoveryPaths map[string]string
	Readers              []ownedcontrol.HistoryReaderRequirement
	LegacySpecs          []ownedcontrol.ServiceSpec
	LegacyWriters        []ownedcontrol.OwnedWriter
	WriterIdentities     []namespaceimport.WriterIdentity
}
type ServicePlanInputs struct {
	Work                     string
	Header                   namespaceimport.Header
	Binaries                 []BinaryArtifact
	LegacyMessageTransfer    BinaryArtifact
	RuntimeConfigDirectory   string
	RuntimeConfigDirectories map[string]string
	LegacyConfigDirectory    string
	RTCEnvironment           map[string]string
	BadgeEvidenceSHA256      string
	LegacyInstanceIDs        map[string]string
}

var fixtureConfigurationMu sync.Mutex

func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func digestValue(v any) string {
	raw, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return digestBytes(raw)
}
func canonicalDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ownedcontrol.ErrOwnedEvidence
	}
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path {
		return ownedcontrol.ErrOwnedEvidence
	}
	info, e := os.Stat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return ownedcontrol.ErrOwnedEvidence
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return ownedcontrol.ErrOwnedEvidence
	}
	return nil
}

// Exact exclusive private custody; no rename-overwrite or in-place repair.
func writePrivate(path string, v any) (string, error) {
	parent := filepath.Dir(path)
	if canonicalDirectory(parent) != nil {
		return "", ownedcontrol.ErrOwnedEvidence
	}
	before, e := os.Lstat(parent)
	if e != nil {
		return "", e
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return "", e
	}
	defer root.Close()
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > 1<<20 {
		return "", ownedcontrol.ErrOwnedEvidence
	}
	raw = append(raw, '\n')
	f, e := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	n, e := f.Write(raw)
	if e == nil && n != len(raw) {
		e = ownedcontrol.ErrOwnedEvidence
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	d, e := root.Open(".")
	if e != nil {
		return "", e
	}
	e = d.Sync()
	ce = d.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	after, e := os.Lstat(parent)
	if e != nil || !os.SameFile(before, after) {
		return "", ownedcontrol.ErrOwnedEvidence
	}
	return digestBytes(raw), nil
}

// This generator is used only by the standalone acceptance supervisor before
// any services are constructed. Use the actual complete config type/decoder;
// do not hand-model a hash of a subset of the application's configuration.
// The mutex/restoration protect repeated preparation of legacy/runtime files;
// callers must not run it inside an already serving application's process.
func withPrivateConfiguration(dir string, body func(string) error) error {
	fixtureConfigurationMu.Lock()
	defer fixtureConfigurationMu.Unlock()
	if canonicalDirectory(dir) != nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	raw, e := namespaceinventory.ReadPrivateDocument(filepath.Join(dir, "config.yaml"), 1<<20)
	if e != nil {
		return e
	}
	notifications, e := namespaceinventory.ReadPrivateDocument(filepath.Join(dir, "notification.yaml"), 1<<20)
	if e != nil {
		return e
	}
	original := config.Config
	defer func() { config.Config = original }()
	reflect.ValueOf(&config.Config).Elem().SetZero()
	if yaml.Unmarshal(raw, &config.Config) != nil || yaml.Unmarshal(notifications, &config.Config.Notification) != nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	encoded, e := json.Marshal(config.Config)
	if e != nil {
		return e
	}
	return body(digestBytes(encoded))
}

func serviceEnvironmentSHA(spec ownedcontrol.ServiceSpec, h namespaceimport.Header, required bool, extra map[string]string) string {
	// These exact original entries are retained by the actual launcher. The
	// source-owned nonce/preparation paths are excluded by the child's original
	// OwnedRPCConfigurationHashes. Managed primary input is intentionally NOT
	// excluded there and therefore must participate in its expected digest.
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + spec.Work}
	if spec.Role == "msgtransfer-managed" {
		env = append(env, spec.BootstrapEnvironmentKey+"="+spec.BootstrapPath, spec.BootstrapSHAEnvironmentKey+"="+spec.BootstrapSHA256)
	}
	if required {
		env = append(env, "LUOBO_GROUP_HISTORY_WINDOW_V1=required", "LUOBO_GROUP_HISTORY_WINDOW_EPOCH="+h.Epoch, "LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256="+h.ActivationSHA256)
	}
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	sort.Strings(env)
	return digestValue(env)
}
func newServiceSpec(base, role string, artifact BinaryArtifact, configDirectory string) (ownedcontrol.ServiceSpec, error) {
	var s ownedcontrol.ServiceSpec
	if role != artifact.Role || artifact.Path == "" || len(artifact.SHA256) != 64 || len(artifact.SourceSHA256) != 64 || len(artifact.ReceiptSHA256) != 64 {
		return s, ownedcontrol.ErrOwnedEvidence
	}
	work := filepath.Join(base, role)
	if e := os.Mkdir(work, 0700); e != nil {
		return s, e
	}
	s = ownedcontrol.ServiceSpec{Role: role, BinaryPath: artifact.Path, BinarySHA256: artifact.SHA256, SourceSHA256: artifact.SourceSHA256, BuildReceiptPath: artifact.ReceiptPath, BuildReceiptSHA256: artifact.ReceiptSHA256, Work: work, Args: []string{"--" + constant.FlagConf, configDirectory}, BootstrapPath: filepath.Join(work, "bootstrap.json"), BootstrapEnvironmentKey: startrpc.OwnedRPCBootstrapEnv, BootstrapSHAEnvironmentKey: startrpc.OwnedRPCBootstrapSHAEnv}
	return s, nil
}
func setPrivateEnvironment(s *ownedcontrol.ServiceSpec, extra map[string]string) error {
	if len(extra) == 0 {
		return nil
	}
	path := filepath.Join(s.Work, "constructor-environment.json")
	sum, e := writePrivate(path, ownedcontrol.OwnedServiceEnvironment{Version: "owned-service-environment-v1", Role: s.Role, Values: extra})
	if e != nil {
		return e
	}
	s.PrivateEnvironmentPath, s.PrivateEnvironmentSHA256 = path, sum
	return nil
}

// PrepareServicePlan writes only fixture/private configuration and selectors.
// It cannot mint a hold, publication, reader registration, process graph or
// release. Real process observations must later match every reviewed selector.
func PrepareServicePlan(in ServicePlanInputs) (ServicePlan, error) {
	out := ServicePlan{ReaderPorts: map[string]int{}, ReaderDiscoveryPaths: map[string]string{}}
	if canonicalDirectory(in.Work) != nil || in.Header.Validate() != nil || len(in.Binaries) != 14 || len(in.BadgeEvidenceSHA256) != 64 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	by := map[string]BinaryArtifact{}
	for _, b := range in.Binaries {
		if _, ok := by[b.Role]; ok {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		by[b.Role] = b
	}
	for _, name := range []string{"legacy", "required"} {
		if e := os.Mkdir(filepath.Join(in.Work, name), 0700); e != nil {
			return out, e
		}
	}
	var policy messagedelivery.KafkaDeliveryPolicy
	var configSHA, registrySHA, groupBootSHA string
	registryPath := filepath.Join(in.Work, "offline-none-registry.json")
	groupBootPath := filepath.Join(in.Work, "group-receiver-input.json")
	e := withPrivateConfiguration(in.RuntimeConfigDirectory, func(sum string) error {
		configSHA = sum
		// Only an explicitly fresh empty provider/callback scope is supported by
		// this no-delivery acceptance fixture. Existing retained obligations are
		// never silently converted to none by this generator.
		if config.Config.Push.Enable != "none" || config.Config.Callback.CallbackOnlinePush.Enable || config.Config.Callback.CallbackOfflinePush.Enable || config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable {
			return ownedcontrol.ErrOwnedEvidence
		}
		var e error
		policy, e = msgtransfer.FreezeOwnedKafkaPolicy(in.Header.Database, in.Header.Epoch, in.Header.ActivationSHA256)
		if e != nil {
			return e
		}
		fingerprint, e := policy.Fingerprint()
		if e != nil {
			return e
		}
		registry := aliasvendor.OwnedOfflinePolicyRegistry{Version: "ordinary-owned-offline-policies-v1", Database: in.Header.Database, Epoch: in.Header.Epoch, ActivationSHA256: in.Header.ActivationSHA256, CurrentPolicySHA256: fingerprint, Entries: []aliasvendor.OwnedOfflinePolicySpec{{Policy: policy, Fingerprint: fingerprint, BindingSeedSHA256: policy.ConfigurationSHA256}}}
		registrySHA, e = writePrivate(registryPath, registry)
		if e != nil {
			return e
		}
		group := msgtransfer.OwnedGroupReceiverBootstrap{Version: "owned-group-receiver-bootstrap-v1", Database: in.Header.Database, Epoch: in.Header.Epoch, ActivationSHA256: in.Header.ActivationSHA256, BadgeEvidenceSHA256: in.BadgeEvidenceSHA256, VendorPath: registryPath, VendorSHA256: registrySHA}
		groupBootSHA, e = writePrivate(groupBootPath, group)
		return e
	})
	if e != nil {
		return out, e
	}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		artifact, ok := by[role]
		if !ok {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		directory := in.RuntimeConfigDirectory
		if selected := in.RuntimeConfigDirectories[role]; selected != "" {
			directory = selected
		}
		roleConfigSHA := ""
		e = withPrivateConfiguration(directory, func(sum string) error {
			roleConfigSHA = sum
			if role == "rpc-conversation" || role == "rpc-msg" {
				ports := config.Config.RpcPort.OpenImConversationPort
				if role == "rpc-msg" {
					ports = config.Config.RpcPort.OpenImMessagePort
				}
				if config.Config.Rpc.ListenIP != "127.0.0.1" || len(ports) != 1 || ports[0] < 1 || ports[0] > 65535 {
					return ownedcontrol.ErrOwnedEvidence
				}
				out.ReaderPorts[role] = ports[0]
				name := config.Config.RpcRegisterName.OpenImConversationName
				if role == "rpc-msg" {
					name = config.Config.RpcRegisterName.OpenImMsgName
				}
				if config.Config.Zookeeper.Schema == "" || strings.ContainsAny(config.Config.Zookeeper.Schema, "/\\") || name == "" || strings.ContainsAny(name, "/\\") {
					return ownedcontrol.ErrOwnedEvidence
				}
				out.ReaderDiscoveryPaths[role] = "/" + config.Config.Zookeeper.Schema + "/" + name
			}
			return nil
		})
		if e != nil {
			return out, e
		}
		s, e := newServiceSpec(filepath.Join(in.Work, "required"), role, artifact, directory)
		if e != nil {
			return out, e
		}
		extra := map[string]string{}
		if role == "rpc-rtc" || role == "api" {
			for k, v := range in.RTCEnvironment {
				extra[k] = v
			}
		}
		if role == "rpc-group" {
			extra["LUOBO_GROUP_OWNED_RECEIVER_INPUT"] = groupBootPath
			extra["LUOBO_GROUP_OWNED_RECEIVER_INPUT_SHA256"] = groupBootSHA
		}
		if e = setPrivateEnvironment(&s, extra); e != nil {
			return out, e
		}
		if role == "msgtransfer-managed" {
			s.BootstrapEnvironmentKey = msgtransfer.OwnedOrdinaryBootstrapPathEnv
			s.BootstrapSHAEnvironmentKey = msgtransfer.OwnedOrdinaryBootstrapSHAEnv
			fingerprint, _ := policy.Fingerprint()
			boot := msgtransfer.OwnedOrdinaryBootstrap{Version: ownedcontrol.NamespaceRuntimeManagedBootstrapVersion, Database: in.Header.Database, Epoch: in.Header.Epoch, ActivationSHA256: in.Header.ActivationSHA256, BadgeEvidenceSHA256: in.BadgeEvidenceSHA256, Topic: "", ConsumerGroup: "", PolicySHA256: fingerprint, VendorPath: registryPath, VendorSHA256: registrySHA, DrainPath: filepath.Join(s.Work, "drain.json")}
			e = withPrivateConfiguration(directory, func(sum string) error {
				if sum != configSHA {
					return ownedcontrol.ErrOwnedEvidence
				}
				boot.Topic = config.Config.Kafka.LatestMsgToRedis.Topic
				boot.ConsumerGroup = config.Config.Kafka.ConsumerGroupID.MsgToRedis
				return nil
			})
			if e != nil {
				return out, e
			}
			s.BootstrapSHA256, e = writePrivate(s.BootstrapPath, boot)
			if e != nil {
				return out, e
			}
		} else {
			envSHA := serviceEnvironmentSHA(s, in.Header, true, extra)
			boot := startrpc.OwnedRPCBootstrap{Version: ownedcontrol.NamespaceRuntimeServerBootstrapVersion, Role: role, Mode: "required", InstanceID: uuid.NewString(), DatabaseSHA256: digestBytes([]byte(in.Header.Database)), Epoch: in.Header.Epoch, ActivationSHA256: in.Header.ActivationSHA256, ConfigurationSHA256: roleConfigSHA, EnvironmentSHA256: envSHA, DrainPath: filepath.Join(s.Work, "drain.json")}
			s.BootstrapSHA256, e = writePrivate(s.BootstrapPath, boot)
			if e != nil {
				return out, e
			}
		}
		r := ownedcontrol.NamespaceRuntimeRequirement{Role: role, BinarySHA256: s.BinarySHA256, SourceSHA256: s.SourceSHA256, BuildReceiptSHA256: s.BuildReceiptSHA256, BootstrapSHA256: s.BootstrapSHA256, ConfigurationSHA256: roleConfigSHA, EnvironmentSHA256: serviceEnvironmentSHA(s, in.Header, true, extra), ContractSHA256: ownedcontrol.NamespaceRuntimeContractSHA256(role), Services: append([]string(nil), artifact.Services...), Components: append([]string(nil), artifact.Components...)}
		if r.ValidateShape() != nil {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		out.Specs = append(out.Specs, s)
		out.Requirements = append(out.Requirements, r)
		if role == "rpc-conversation" || role == "rpc-msg" {
			out.Readers = append(out.Readers, ownedcontrol.HistoryReaderRequirement{Role: role, BinarySHA256: r.BinarySHA256, SourceSHA256: r.SourceSHA256, BuildReceiptSHA256: r.BuildReceiptSHA256, BootstrapSHA256: r.BootstrapSHA256, ConfigurationSHA256: r.ConfigurationSHA256, EnvironmentSHA256: r.EnvironmentSHA256, Services: r.Services, ReadContractSHA256: messagedelivery.HistoryReaderContractSHA256()})
		}
	}
	if _, e = ownedcontrol.NamespaceRuntimeRequirementsSHA256(out.Requirements); e != nil {
		return out, e
	}
	if _, e = ownedcontrol.HistoryReaderRequirementsSHA256(out.Readers); e != nil {
		return out, e
	}
	e = withPrivateConfiguration(in.LegacyConfigDirectory, func(configuration string) error {
		for at, role := range []string{"rpc-msg", "msgtransfer-legacy"} {
			artifact := by[role]
			if role == "msgtransfer-legacy" {
				artifact = in.LegacyMessageTransfer
			}
			s, e := newServiceSpec(filepath.Join(in.Work, "legacy"), role, artifact, in.LegacyConfigDirectory)
			if e != nil {
				return e
			}
			instance := in.LegacyInstanceIDs[role]
			parsed, e := uuid.Parse(instance)
			if e != nil || parsed == uuid.Nil || parsed.String() != instance {
				return ownedcontrol.ErrOwnedEvidence
			}
			boot := startrpc.OwnedRPCBootstrap{Version: "owned-server-bootstrap-v1", Role: role, Mode: "legacy", InstanceID: instance, DatabaseSHA256: digestBytes([]byte(in.Header.Database)), ConfigurationSHA256: configuration, EnvironmentSHA256: serviceEnvironmentSHA(s, in.Header, false, nil), DrainPath: filepath.Join(s.Work, "drain.json")}
			s.BootstrapSHA256, e = writePrivate(s.BootstrapPath, boot)
			if e != nil {
				return e
			}
			phase := "producer"
			if at == 1 {
				phase = "consumer"
			}
			deployment := "fresh-acceptance-" + in.Header.MigrationID + "-" + role
			out.LegacySpecs = append(out.LegacySpecs, s)
			out.LegacyWriters = append(out.LegacyWriters, ownedcontrol.OwnedWriter{Role: role, Deployment: deployment, BinarySHA256: s.BinarySHA256, SourceSHA256: s.SourceSHA256, BootstrapSHA256: s.BootstrapSHA256, RuntimeMode: "legacy", Phase: phase})
			out.WriterIdentities = append(out.WriterIdentities, namespaceimport.WriterIdentity{Deployment: deployment, Incarnation: instance, BinarySHA256: s.BinarySHA256, SourceSHA256: s.SourceSHA256})
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	// All source files are re-read by the actual child and original launcher.
	// Preparation returns no successful process/SQL/provider result.
	return out, nil
}

// Keep a byte equality check available to the fixture collector without
// weakening the actual launcher's independent private-file validation.
func exactJSON(a, b any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}
