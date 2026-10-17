//go:build linux

package runtimeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	pbpush "github.com/OpenIMSDK/protocol/push"
	"github.com/OpenIMSDK/protocol/sdkws"
	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type AcceptedRole struct {
	Role               string   `json:"role"`
	PID                int      `json:"pid"`
	StartToken         string   `json:"startToken"`
	BinarySHA256       string   `json:"binarySHA256"`
	SourceSHA256       string   `json:"sourceSHA256"`
	OwnerNonceSHA256   string   `json:"ownerNonceSHA256"`
	AnnouncementSHA256 string   `json:"announcementSHA256"`
	DrainSHA256        string   `json:"drainSHA256,omitempty"`
	ActualAbsent       bool     `json:"actualAbsent"`
	JoinedAndClosed    []string `json:"joinedAndClosed,omitempty"`
	CanonicalDrain     bool     `json:"canonicalDrain"`
}
type AcceptanceResult struct {
	Version                  string                     `json:"version"`
	Scenario                 string                     `json:"scenario"`
	Phase                    string                     `json:"phase"`
	BootstrapStage           BootstrapStage             `json:"bootstrapStage,omitempty"`
	BootstrapFailureTag      string                     `json:"bootstrapFailureTag,omitempty"`
	Accepted                 bool                       `json:"accepted"`
	ResourcePlaneSHA256      string                     `json:"resourcePlaneSHA256"`
	HeaderSHA256             string                     `json:"headerSHA256,omitempty"`
	SourceSHA256             string                     `json:"sourceSHA256,omitempty"`
	SignedInventorySHA256    string                     `json:"signedInventorySHA256,omitempty"`
	ImportManifestSHA256     string                     `json:"importManifestSHA256,omitempty"`
	DataReadySHA256          string                     `json:"dataReadySHA256,omitempty"`
	KnownAdmissionSHA256     string                     `json:"knownAdmissionSHA256,omitempty"`
	ActualStartedRoles       int                        `json:"actualStartedRoles"`
	LegacyStartupStage       string                     `json:"legacyStartupStage,omitempty"`
	LegacyChildRetained      bool                       `json:"legacyChildRetained"`
	AcquireStage             string                     `json:"acquireStage,omitempty"`
	AcquireAttempted         bool                       `json:"acquireAttempted"`
	AcquireReturned          bool                       `json:"acquireReturned"`
	AcquireErrorClass        string                     `json:"acquireErrorClass,omitempty"`
	LocalCleanupObserved     bool                       `json:"localCleanupObserved"`
	LocalCleanupState        string                     `json:"localCleanupState,omitempty"`
	LocalCleanupFailureClass string                     `json:"localCleanupFailureClass,omitempty"`
	ReaderPhases             []ReaderPhaseObservation   `json:"readerPhases"`
	ReaderServed             []ActualReaderEndpoint     `json:"readerServed"`
	Cancellation             *CancellationObservation   `json:"cancellation,omitempty"`
	Roles                    []AcceptedRole             `json:"roles"`
	RawPushRejected          bool                       `json:"rawPushRejected"`
	ActualUserReadExact      bool                       `json:"actualUserReadExact"`
	ActualTokenRPCDispatched int                        `json:"actualTokenRPCDispatched"`
	ActualTokenRPCSucceeded  bool                       `json:"actualTokenRPCSucceeded"`
	RedisKeyPresentBefore    bool                       `json:"redisKeyPresentBefore"`
	RedisKeyAbsentAfter      bool                       `json:"redisKeyAbsentAfter"`
	ACKLoss                  RedisACKLossObservation    `json:"ackLoss"`
	CanonicalShutdown        bool                       `json:"canonicalShutdown"`
	UnknownOwnerRetained     bool                       `json:"unknownOwnerRetained"`
	MongoMessageCount        int64                      `json:"mongoMessageCount"`
	FreshPlaneOnly           bool                       `json:"freshPlaneOnly"`
	BusinessPermissions      BusinessPermissionEvidence `json:"businessPermissions"`
	FailureClass             string                     `json:"failureClass,omitempty"`
	FailureSHA256            string                     `json:"failureSHA256,omitempty"`
}
type originalRole struct {
	service      *ownedcontrol.OwnedService
	owner        ownedcontrol.ProcessOwner
	announcement ownedcontrol.RuntimeAnnouncement
}

// observeBootstrapFailure classifies only the original PrepareFresh error.
// Labels are observations, not unique root causes; provider text stays private.
func observeBootstrapFailure(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancel"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	}
	var command mongo.CommandError
	if errors.As(err, &command) {
		switch command.Code {
		case 18:
			return "mongo-auth"
		case 13:
			return "mongo-unauthorized"
		case 10107, 13435, 10058:
			return "mongo-not-primary"
		case 48:
			return "mongo-namespace-exists"
		default:
			return "mongo-other"
		}
	}
	var broker sarama.KError
	if errors.As(err, &broker) {
		switch broker {
		case sarama.ErrConsumerCoordinatorNotAvailable:
			return "kafka-coordinator-unavailable"
		case sarama.ErrSASLAuthenticationFailed:
			return "kafka-auth"
		case sarama.ErrTopicAuthorizationFailed, sarama.ErrClusterAuthorizationFailed:
			return "kafka-topic-or-cluster-authorization"
		case sarama.ErrUnsupportedVersion:
			return "kafka-unsupported-version"
		default:
			return "kafka-other"
		}
	}
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission"
	case errors.Is(err, syscall.ENOSPC):
		return "no-space"
	case errors.Is(err, os.ErrNotExist):
		return "not-found"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection-refused"
	default:
		return "other"
	}
}

// Only local retained Wait/Close outcomes are summarized here, never raw errors
// or a canonical business drain. The original error remains joined unchanged.
func observeLocalCleanupFailure(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ownedcontrol.ErrOwnedEvidence):
		return "owned-evidence"
	default:
		return "other"
	}
}

// RunAcceptance is one non-retrying scenario in one freshly created plane.
// Both success and failure are collected from actual source processes; a
// missing fixture, malformed bootstrap or failed startup never passes a case.
func RunAcceptance(ctx context.Context, plan PrivatePlan) (result AcceptanceResult, runErr error) {
	result = AcceptanceResult{Version: "owned-fullgraph-token-v2-result-v1", Scenario: plan.Scenario, Phase: "actual-resource-bootstrap", ResourcePlaneSHA256: plan.PlaneSHA256, FreshPlaneOnly: true}
	plan.bootstrapStage = &result.BootstrapStage
	boot, e := PrepareFresh(ctx, plan)
	if e != nil {
		result.BootstrapFailureTag = observeBootstrapFailure(e)
	}
	var session *Session
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 120*time.Second)
		defer stop()
		localCleanupErr := cleanupAcceptanceSession(cleanup, session)
		runErr = errors.Join(runErr, localCleanupErr)
		if boot != nil {
			runErr = errors.Join(runErr, boot.Close(cleanup))
		}
		result.LocalCleanupState = "no-session"
		if session != nil {
			result.LocalCleanupState = "incomplete"
			if session.CleanupComplete() {
				result.LocalCleanupState = "complete"
			}
		}
		result.LocalCleanupFailureClass = observeLocalCleanupFailure(localCleanupErr)
		result.LocalCleanupObserved = true
		if runErr != nil {
			result.Accepted = false
			result.FailureSHA256 = digestBytes([]byte(runErr.Error()))
			result.FailureClass = "source-error-private"
			if errors.Is(runErr, ownedcontrol.ErrOwnedEvidence) {
				result.FailureClass = "owned-evidence-rejected"
			}
			if errors.Is(runErr, namespaceimport.ErrCommitUnconfirmed) {
				result.FailureClass = "commit-unconfirmed"
			}
			if errors.Is(runErr, context.DeadlineExceeded) {
				result.FailureClass = "deadline"
			}
			if errors.Is(runErr, context.Canceled) {
				result.FailureClass = "cancelled"
			}
		}
		// This finite result has no config, credentials, raw messages, raw provider
		// text or nonces. Every private custody object remains outside collection.
		if _, e := writePrivate(filepath.Join(plan.Work, "result.json"), result); e != nil {
			runErr = errors.Join(runErr, e)
			result.Accepted = false
		}
	}()
	if e != nil {
		return result, e
	}
	result.HeaderSHA256 = boot.Header.SHA256()
	result.SourceSHA256 = boot.Services.Requirements[0].SourceSHA256
	result.BusinessPermissions = boot.BusinessPermissions
	result.Phase = "actual-hold-import-and-fourteen-original-processes"
	session, e = Start(ctx, boot.Inputs)
	if session != nil {
		result.LegacyStartupStage = session.legacyStartupStage
		result.LegacyChildRetained = session.legacyChildRetained
		result.AcquireStage = session.acquireObservation.Stage
		result.AcquireAttempted = session.acquireObservation.Attempted
		result.AcquireReturned = session.acquireObservation.Returned
		result.AcquireErrorClass = session.acquireObservation.ErrorClass
	}
	if e != nil {
		if session != nil {
			result.Phase = session.Phase
		}
		return result, e
	}
	result.ActualStartedRoles = len(session.Runtime.Started)
	result.ReaderPhases = session.Runtime.ReaderPhases
	result.ReaderServed = session.Runtime.ReaderServed
	if boot.Inputs.StopAt != "" {
		result.SignedInventorySHA256 = session.State.Inventory.SHA256()
		result.ImportManifestSHA256 = session.State.Cutover.ManifestSHA256
		result.DataReadySHA256 = session.Runtime.DataReadySHA256
		observation, e := collectActualCancellation(ctx, boot, session)
		result.Cancellation = &observation
		result.ReaderPhases = session.Runtime.ReaderPhases
		if e != nil {
			return result, e
		}
		result.Phase = "actual-prerelease-cancel-and-original-joins-accepted"
		result.Accepted = true
		return result, nil
	}
	if result.ActualStartedRoles != 14 {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.SignedInventorySHA256 = session.State.Inventory.SHA256()
	result.ImportManifestSHA256 = session.State.Cutover.ManifestSHA256
	result.DataReadySHA256 = session.Runtime.DataReadySHA256
	result.KnownAdmissionSHA256 = session.Runtime.AdmissionSHA256
	processes, e := session.Runtime.Processes.Processes()
	if e != nil {
		return result, e
	}
	original := map[string]originalRole{}
	started := map[string]ownedcontrol.NamespaceRuntimeStartedObservation{}
	for _, r := range session.Runtime.Started {
		started[r.Runtime.Role] = r
	}
	for _, p := range processes {
		owner, a, e := p.Service.AwaitAnnouncement(ctx)
		if e != nil {
			return result, e
		}
		r := started[owner.Role]
		if r.Runtime.PID != owner.PID || r.Runtime.StartToken != owner.StartToken || r.Runtime.OwnerNonceSHA256 != digestBytes([]byte(owner.OwnerNonce)) || owner.BootstrapSHA256 != p.Bootstrap.Requirement.BootstrapSHA256 {
			return result, ownedcontrol.ErrOwnedEvidence
		}
		original[owner.Role] = originalRole{p.Service, owner, a}
	}
	if _, e = writePrivate(filepath.Join(plan.Work, "original-role-owners.json"), func() any {
		rows := []ownedcontrol.ProcessOwner{}
		for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
			rows = append(rows, original[role].owner)
		}
		return rows
	}()); e != nil {
		return result, e
	}
	result.Phase = "actual-required-push-rpc"
	conn, e := grpc.NewClient("127.0.0.1:"+strconv.Itoa(boot.Ports["push"]), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
	if e != nil {
		return result, e
	}
	defer conn.Close()
	push := pbpush.NewPushMsgServiceClient(conn)
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	call = context.WithValue(call, constant.OperationID, uuid.NewString())
	call = context.WithValue(call, constant.OpUserID, boot.ManagerID)
	call = context.WithValue(call, constant.OpUserPlatform, "5")
	userConn, e := grpc.NewClient("127.0.0.1:"+strconv.Itoa(boot.Ports["rpc-user"]), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
	if e != nil {
		return result, e
	}
	defer userConn.Close()
	users, e := pbuser.NewUserClient(userConn).GetDesignateUsers(call, &pbuser.GetDesignateUsersReq{UserIDs: []string{boot.ManagerID}}, grpc.WaitForReady(true))
	if e != nil || users == nil || len(users.UsersInfo) != 1 || users.UsersInfo[0] == nil || users.UsersInfo[0].UserID != boot.ManagerID {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.ActualUserReadExact = true
	// A valid raw RPC reaches the real required-mode method and must fail. No
	// invalid-request shortcut, replacement health server or synthetic admission.
	raw := &pbpush.PushMsgReq{MsgData: &sdkws.MsgData{SendID: boot.ManagerID, RecvID: boot.TokenUserID, ClientMsgID: uuid.NewString(), ServerMsgID: uuid.NewString(), SessionType: constant.SingleChatType, ContentType: constant.Text, Content: []byte(`{"content":"synthetic raw path must be refused"}`), SendTime: time.Now().UnixMilli(), CreateTime: time.Now().UnixMilli(), Options: map[string]bool{constant.IsHistory: true, constant.IsPersistent: true}}}
	if _, e = push.PushMsg(call, raw, grpc.WaitForReady(true)); e == nil || !strings.Contains(e.Error(), "original owned legacy push is unconfirmed") {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.RawPushRejected = true
	key := "FCM_TOKEN:" + boot.TokenUserID + ":" + strconv.Itoa(int(boot.TokenPlatform))
	if e = boot.AdminRedis.Set(ctx, key, "synthetic-one-shot-fullgraph-token", time.Minute).Err(); e != nil {
		return result, e
	}
	exists, e := boot.AdminRedis.Exists(ctx, key).Result()
	if e != nil || exists != 1 {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.RedisKeyPresentBefore = true
	if plan.Scenario == "redis-unknown" {
		if e = boot.Proxy.Arm(); e != nil {
			return result, e
		}
	}
	// Exactly one actual original RPC dispatch. A failure/timeout/unknown never
	// reaches a retry loop and does not issue a second token mutation.
	result.ActualTokenRPCDispatched = 1
	_, tokenErr := push.DelUserPushToken(call, &pbpush.DelUserPushTokenReq{UserID: boot.TokenUserID, PlatformID: boot.TokenPlatform})
	result.ActualTokenRPCSucceeded = tokenErr == nil
	if plan.Scenario == "ack" && tokenErr != nil || plan.Scenario == "redis-unknown" && tokenErr == nil {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	exists, e = boot.AdminRedis.Exists(ctx, key).Result()
	if e != nil || exists != 0 {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.RedisKeyAbsentAfter = true
	result.ACKLoss = boot.Proxy.Observation()
	if plan.Scenario == "redis-unknown" {
		if result.ACKLoss.ExactDeleteRequests != 1 || !result.ACKLoss.ActualUpstreamIntegerOne || !result.ACKLoss.ActualReplyDiscarded || result.ACKLoss.TransportFailure {
			return result, ownedcontrol.ErrOwnedEvidence
		}
	} else if result.ACKLoss.ExactDeleteRequests != 0 || result.ACKLoss.ActualReplyDiscarded {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.Phase = "original-fourteen-stop-and-independent-collection"
	closeContext, closeCancel := context.WithTimeout(context.Background(), 120*time.Second)
	shutdownErr := session.Cleanup(closeContext)
	closeCancel()
	result.CanonicalShutdown = shutdownErr == nil
	if plan.Scenario == "ack" && shutdownErr != nil || plan.Scenario == "redis-unknown" && shutdownErr == nil {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		r := original[role]
		observed := started[role]
		pub := AcceptedRole{Role: role, PID: r.owner.PID, StartToken: r.owner.StartToken, BinarySHA256: r.owner.BinarySHA256, SourceSHA256: r.owner.SourceSHA256, OwnerNonceSHA256: digestBytes([]byte(r.owner.OwnerNonce)), AnnouncementSHA256: observed.AnnouncementSHA256}
		if _, e = os.Stat("/proc/" + strconv.Itoa(r.owner.PID)); !os.IsNotExist(e) {
			result.Roles = append(result.Roles, pub)
			return result, ownedcontrol.ErrOwnedEvidence
		}
		pub.ActualAbsent = true
		// This method cannot signal: OS absence was just independently verified,
		// and the retained original done/join must already have completed.
		joined, e := r.service.StopAndJoin(ctx)
		if role == "push" && plan.Scenario == "redis-unknown" {
			if e == nil {
				return result, ownedcontrol.ErrOwnedEvidence
			}
			if _, e = os.Lstat(r.announcement.DrainPath); !os.IsNotExist(e) {
				return result, ownedcontrol.ErrOwnedEvidence
			}
			body, e := namespaceinventory.ReadPrivateDocument(r.announcement.DrainPath+".unconfirmed", 1<<20)
			if e != nil {
				return result, e
			}
			var source struct {
				Schema      int                            `json:"schema"`
				OwnerNonce  string                         `json:"ownerNonce"`
				Observation ownedcontrol.SourceObservation `json:"observation"`
			}
			if namespaceinventory.DecodeStrict(body, &source) != nil || source.Schema != 1 || source.OwnerNonce != r.owner.OwnerNonce || source.Observation.Role != "push" || source.Observation.InstanceID != r.announcement.InstanceID || source.Observation.BootstrapSHA256 != r.owner.BootstrapSHA256 || source.Observation.PolicySHA256 != r.announcement.PolicySHA256 || !source.Observation.SourceOutcomeUnconfirmed || !source.Observation.External.RedisMutation.Unknown || source.Observation.External.RedisMutation.Started != 1 || source.Observation.External.RedisMutation.Unconfirmed != 1 {
				return result, ownedcontrol.ErrOwnedEvidence
			}
			result.UnknownOwnerRetained = true
		} else {
			if e != nil || joined.Owner != r.owner || joined.AnnouncementSHA256 != observed.AnnouncementSHA256 || joined.DrainSHA256 == "" {
				return result, ownedcontrol.ErrOwnedEvidence
			}
			pub.CanonicalDrain = true
			pub.DrainSHA256 = joined.DrainSHA256
			pub.JoinedAndClosed = append([]string(nil), r.announcement.OwnedStopStepNames...)
			if role == "push" {
				want := []string{"rpc-handlers", "rpc-serve", "business-workers", "redis", "discovery"}
				if !reflect.DeepEqual(pub.JoinedAndClosed, want) {
					return result, ownedcontrol.ErrOwnedEvidence
				}
			}
		}
		result.Roles = append(result.Roles, pub)
	}
	messages, e := boot.AdminMongo.Database(boot.Header.Database).Collection("msg").CountDocuments(ctx, bson.D{})
	if e != nil || messages != 0 {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	result.MongoMessageCount = messages
	if plan.Scenario == "redis-unknown" && !result.UnknownOwnerRetained {
		return result, ownedcontrol.ErrOwnedEvidence
	}
	if e = boot.Resource.ValidateLive(ctx); e != nil {
		return result, e
	}
	sort.Slice(result.Roles, func(i, j int) bool { return result.Roles[i].Role < result.Roles[j].Role })
	result.Phase = "actual-token-v2-and-original-joins-accepted"
	result.Accepted = true
	return result, nil
}

// Private result parser for the original directly affected test. Reading a
// result is never a capability for production startup or history access.
func ReadResult(path string) (AcceptanceResult, error) {
	var r AcceptanceResult
	raw, e := namespaceinventory.ReadPrivateDocument(path, 1<<20)
	if e != nil || json.Unmarshal(raw, &r) != nil || r.Version != "owned-fullgraph-token-v2-result-v1" {
		return r, ownedcontrol.ErrOwnedEvidence
	}
	return r, nil
}

// Used by the real RunAcceptance defer, including all source/audit early exits.
// Canonical failure and local completion are deliberately different states.
func cleanupAcceptanceSession(ctx context.Context, s *Session) error {
	if s == nil || s.CleanupComplete() {
		return nil
	}
	return s.Cleanup(ctx)
}
