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
func (p *KafkaProbe) ExcludeLegacy(ctx context.Context, hold *AdmissionHold, old KafkaLegacyCredential, apply bool) (out KafkaRevocation, resultErr error) {
	if p == nil || ctx == nil || hold.Validate() != nil || !strings.HasPrefix(old.User, "owned_legacy_") || old.User == p.user || old.Password == "" || !isSHA(old.PrincipalVersion) {
		return out, ErrOwnedEvidence
	}
	client, e := sarama.NewClient(p.brokers, p.config())
	if e != nil {
		return out, e
	}
	admin, e := sarama.NewClusterAdminFromClient(client)
	if e != nil {
		client.Close()
		return out, e
	}
	defer func() {
		if admin.Close() != nil {
			out = KafkaRevocation{}
			resultErr = ErrOwnedEvidence
		}
	}()
	controller, e := client.Controller()
	if e != nil {
		return out, e
	}
	metadata, e := controller.GetMetadata(&sarama.MetadataRequest{Version: 7, AllowAutoTopicCreation: false})
	if e != nil || metadata.ClusterID == nil || *metadata.ClusterID != p.cluster || len(metadata.Brokers) == 0 {
		return out, ErrOwnedEvidence
	}
	principal := "User:" + old.User
	acl := sarama.Acl{Principal: principal, Host: "*", Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny}
	for _, resource := range kafkaDenyResources() {
		if ctx.Err() != nil || hold.Validate() != nil {
			return out, ErrOwnedEvidence
		}
		if apply {
			if e = admin.CreateACL(resource, acl); e != nil {
				return out, e
			}
		}
		name := resource.ResourceName
		host := "*"
		rows, e := admin.ListAcls(sarama.AclFilter{Version: 1, ResourceType: resource.ResourceType, ResourceName: &name, ResourcePatternTypeFilter: sarama.AclPatternLiteral, Principal: &principal, Host: &host, Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny})
		if e != nil || !exactDeny(rows, resource, principal) {
			return out, ErrOwnedEvidence
		}
	}
	// Open the original legacy SASL credentials and demand a broker-produced
	// authorization error. This rejects auth-disabled/super-user/ignored ACL
	// fixtures rather than certifying them from a static configuration string.
	cfg := p.config()
	cfg.Net.SASL.User = old.User
	cfg.Net.SASL.Password = old.Password
	legacy, e := sarama.NewClient(p.brokers, cfg)
	if e != nil {
		return out, e
	}
	defer func() {
		if legacy.Close() != nil {
			out = KafkaRevocation{}
			resultErr = ErrOwnedEvidence
		}
	}()
	denied := []int32{}
	for _, broker := range metadata.Brokers {
		if ctx.Err() != nil || !loopbackBroker(broker.Addr()) || hold.Validate() != nil {
			return out, ErrOwnedEvidence
		}
		target, e := legacy.Broker(broker.ID())
		if e != nil || target.Addr() != broker.Addr() {
			return out, ErrOwnedEvidence
		}
		if e = target.Open(cfg); e != nil && e != sarama.ErrAlreadyConnected {
			return out, e
		}
		reply, e := target.DescribeConfigs(&sarama.DescribeConfigsRequest{Version: 2, Resources: []*sarama.ConfigResource{{Type: sarama.BrokerResource, Name: fmt.Sprint(broker.ID()), ConfigNames: []string{"log.retention.ms"}}}})
		if e != nil || len(reply.Resources) != 1 || reply.Resources[0].Name != fmt.Sprint(broker.ID()) || reply.Resources[0].ErrorCode != int16(sarama.ErrClusterAuthorizationFailed) {
			return out, ErrOwnedEvidence
		}
		denied = append(denied, broker.ID())
	}
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
	return KafkaRevocation{principal, old.PrincipalVersion, sha(body), sha(denialBody), len(denied)}, nil
}
