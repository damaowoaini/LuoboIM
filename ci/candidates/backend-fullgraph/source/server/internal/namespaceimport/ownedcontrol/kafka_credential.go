//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IBM/sarama"
)

// This adapter is limited to source-owned SASL/PLAIN brokers. ACLs permanently
// deny the legacy principal, including pre-existing authenticated connections.
// Metadata success or a network error is not an authorization denial.
type KafkaLegacyCredential struct {
	User, Password   string
	PrincipalVersion string
}
type KafkaRevocation struct {
	PrincipalID, Version, ACLsSHA256, LiveDenialsSHA256 string
	Brokers                                             int
}

func kafkaDenyResources() []sarama.Resource {
	return []sarama.Resource{
		{ResourceType: sarama.AclResourceTopic, ResourceName: "*", ResourcePatternType: sarama.AclPatternLiteral},
		{ResourceType: sarama.AclResourceGroup, ResourceName: "*", ResourcePatternType: sarama.AclPatternLiteral},
		{ResourceType: sarama.AclResourceTransactionalID, ResourceName: "*", ResourcePatternType: sarama.AclPatternLiteral},
		{ResourceType: sarama.AclResourceCluster, ResourceName: "kafka-cluster", ResourcePatternType: sarama.AclPatternLiteral},
	}
}
func exactDeny(rows []sarama.ResourceAcls, resource sarama.Resource, principal string) bool {
	for _, r := range rows {
		if r.Resource != resource {
			continue
		}
		for _, a := range r.Acls {
			if a != nil && a.Principal == principal && a.Host == "*" && a.Operation == sarama.AclOperationAll && a.PermissionType == sarama.AclPermissionDeny {
				return true
			}
		}
	}
	return false
}

// Sarama 1.41.3 ClusterAdmin helpers return transport errors but discard these
// broker-produced ACL errors. Keep the original response and fail closed before
// exactDeny; neither an empty list nor a nil transport error proves permission.
func kafkaACLCreateResponseError(reply *sarama.CreateAclsResponse, callErr error) error {
	if callErr != nil {
		return callErr
	}
	if reply == nil || len(reply.AclCreationResponses) != 1 || reply.AclCreationResponses[0] == nil {
		return ErrOwnedEvidence
	}
	if e := reply.AclCreationResponses[0].Err; e != sarama.ErrNoError {
		return e
	}
	return nil
}
func kafkaACLDescribeResponseRows(reply *sarama.DescribeAclsResponse, callErr error) ([]sarama.ResourceAcls, error) {
	if callErr != nil {
		return nil, callErr
	}
	if reply == nil {
		return nil, ErrOwnedEvidence
	}
	if reply.Err != sarama.ErrNoError {
		return nil, reply.Err
	}
	var rows []sarama.ResourceAcls
	for _, row := range reply.ResourceAcls {
		if row == nil {
			return nil, ErrOwnedEvidence
		}
		rows = append(rows, *row)
	}
	return rows, nil
}

// Optional trace of one original call; no identity, error text or authority.
type kafkaExclusionObservation struct {
	stage string
}

func (o *kafkaExclusionObservation) enter(stage string) {
	if o != nil {
		o.stage = stage
	}
}
func (o *kafkaExclusionObservation) resource(resource sarama.Resource, step string) {
	if o == nil {
		return
	}
	var kind string
	switch resource.ResourceType {
	case sarama.AclResourceTopic:
		kind = "topic"
	case sarama.AclResourceGroup:
		kind = "group"
	case sarama.AclResourceTransactionalID:
		kind = "transaction"
	case sarama.AclResourceCluster:
		kind = "cluster"
	default:
		o.stage = "acl-resource"
		return
	}
	o.stage = kind + "-" + step
}

func (p *KafkaProbe) ExcludeLegacy(ctx context.Context, hold *AdmissionHold, old KafkaLegacyCredential, apply bool) (out KafkaRevocation, resultErr error) {
	return p.excludeLegacy(ctx, hold, old, apply, nil)
}

func (p *KafkaProbe) excludeLegacy(ctx context.Context, hold *AdmissionHold, old KafkaLegacyCredential, apply bool, observation *kafkaExclusionObservation) (out KafkaRevocation, resultErr error) {
	observation.enter("input")
	if p == nil || ctx == nil || hold.Validate() != nil || !strings.HasPrefix(old.User, "owned_legacy_") || old.User == p.user || old.Password == "" || !isSHA(old.PrincipalVersion) {
		return out, ErrOwnedEvidence
	}
	observation.enter("admin-client")
	client, e := sarama.NewClient(p.brokers, p.config())
	if e != nil {
		return out, e
	}
	observation.enter("admin-handle")
	admin, e := sarama.NewClusterAdminFromClient(client)
	if e != nil {
		client.Close()
		return out, e
	}
	defer func() {
		if admin.Close() != nil {
			observation.enter("admin-close")
			out = KafkaRevocation{}
			resultErr = ErrOwnedEvidence
		}
	}()
	observation.enter("metadata-controller")
	controller, e := client.Controller()
	if e != nil {
		return out, e
	}
	observation.enter("metadata")
	metadata, e := controller.GetMetadata(&sarama.MetadataRequest{Version: 7, AllowAutoTopicCreation: false})
	if e != nil || metadata.ClusterID == nil || *metadata.ClusterID != p.cluster || len(metadata.Brokers) == 0 {
		return out, ErrOwnedEvidence
	}
	principal := "User:" + old.User
	acl := sarama.Acl{Principal: principal, Host: "*", Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny}
	for _, resource := range kafkaDenyResources() {
		observation.resource(resource, "guard")
		if ctx.Err() != nil || hold.Validate() != nil {
			return out, ErrOwnedEvidence
		}
		if apply {
			observation.resource(resource, "write-controller")
			aclBroker, callErr := admin.Controller()
			if callErr != nil {
				return out, callErr
			}
			observation.resource(resource, "write-call")
			reply, callErr := aclBroker.CreateAcls(&sarama.CreateAclsRequest{Version: 1, AclCreations: []*sarama.AclCreation{{Resource: resource, Acl: acl}}})
			if observation != nil && callErr == nil {
				observation.resource(resource, "write-response")
			}
			if e = kafkaACLCreateResponseError(reply, callErr); e != nil {
				return out, e
			}
		}
		name := resource.ResourceName
		host := "*"
		observation.resource(resource, "read-controller")
		aclBroker, callErr := admin.Controller()
		if callErr != nil {
			return out, ErrOwnedEvidence
		}
		observation.resource(resource, "read-call")
		reply, callErr := aclBroker.DescribeAcls(&sarama.DescribeAclsRequest{Version: 1, AclFilter: sarama.AclFilter{Version: 1, ResourceType: resource.ResourceType, ResourceName: &name, ResourcePatternTypeFilter: sarama.AclPatternLiteral, Principal: &principal, Host: &host, Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny}})
		if observation != nil && callErr == nil {
			observation.resource(resource, "read-response")
		}
		rows, e := kafkaACLDescribeResponseRows(reply, callErr)
		if callErr != nil {
			return out, ErrOwnedEvidence
		}
		if e != nil {
			return out, e
		}
		observation.resource(resource, "exact-deny")
		if !exactDeny(rows, resource, principal) {
			return out, ErrOwnedEvidence
		}
	}
	// Open the original legacy SASL credentials and demand a broker-produced
	// authorization error. This rejects auth-disabled/super-user/ignored ACL
	// fixtures rather than certifying them from a static configuration string.
	cfg := p.config()
	cfg.Net.SASL.User = old.User
	cfg.Net.SASL.Password = old.Password
	observation.enter("legacy-client")
	legacy, e := sarama.NewClient(p.brokers, cfg)
	if e != nil {
		return out, e
	}
	defer func() {
		if legacy.Close() != nil {
			observation.enter("legacy-close")
			out = KafkaRevocation{}
			resultErr = ErrOwnedEvidence
		}
	}()
	denied := []int32{}
	for _, broker := range metadata.Brokers {
		observation.enter("broker-input")
		if ctx.Err() != nil || !loopbackBroker(broker.Addr()) || hold.Validate() != nil {
			return out, ErrOwnedEvidence
		}
		observation.enter("broker-handle")
		target, e := legacy.Broker(broker.ID())
		if e != nil || target.Addr() != broker.Addr() {
			return out, ErrOwnedEvidence
		}
		observation.enter("broker-open")
		if e = target.Open(cfg); e != nil && e != sarama.ErrAlreadyConnected {
			return out, e
		}
		observation.enter("live-denial")
		reply, e := target.DescribeConfigs(&sarama.DescribeConfigsRequest{Version: 2, Resources: []*sarama.ConfigResource{{Type: sarama.BrokerResource, Name: fmt.Sprint(broker.ID()), ConfigNames: []string{"log.retention.ms"}}}})
		if e != nil || len(reply.Resources) != 1 || reply.Resources[0].Name != fmt.Sprint(broker.ID()) || reply.Resources[0].ErrorCode != int16(sarama.ErrClusterAuthorizationFailed) {
			return out, ErrOwnedEvidence
		}
		denied = append(denied, broker.ID())
	}
	observation.enter("evidence")
	value := struct {
		Cluster, Principal, Version string
		Resources                   []sarama.Resource
		ACL                         sarama.Acl
	}{p.cluster, principal, old.PrincipalVersion, kafkaDenyResources(), acl}
	body, e := json.Marshal(value)
	if e != nil {
		return out, e
	}
	denialBody, e := json.Marshal(struct {
		Cluster   string
		BrokerIDs []int32
		Code      int16
	}{p.cluster, denied, int16(sarama.ErrClusterAuthorizationFailed)})
	if e != nil {
		return out, e
	}
	observation.enter("complete")
	return KafkaRevocation{principal, old.PrincipalVersion, sha(body), sha(denialBody), len(denied)}, nil
}
