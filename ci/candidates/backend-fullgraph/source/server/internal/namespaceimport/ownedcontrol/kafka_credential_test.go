//go:build linux

package ownedcontrol

import (
	"errors"
	"github.com/IBM/sarama"
	"testing"
)

func TestOwnedKafkaExclusionRequiresEveryWildcardDeny(t *testing.T) {
	resources := kafkaDenyResources()
	if len(resources) != 4 {
		t.Fatal("topic/group/transaction/cluster exclusion incomplete")
	}
	seen := map[sarama.AclResourceType]bool{}
	for _, r := range resources {
		if seen[r.ResourceType] || r.ResourcePatternType != sarama.AclPatternLiteral || r.ResourceType != sarama.AclResourceCluster && r.ResourceName != "*" {
			t.Fatal("resource is not exact full old-principal exclusion")
		}
		seen[r.ResourceType] = true
		good := sarama.Acl{Principal: "User:owned_legacy_fixture", Host: "*", Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny}
		rows := []sarama.ResourceAcls{{Resource: r, Acls: []*sarama.Acl{&good}}}
		if !exactDeny(rows, r, good.Principal) {
			t.Fatal("real exact deny rejected")
		}
		bad := good
		bad.PermissionType = sarama.AclPermissionAllow
		rows[0].Acls = []*sarama.Acl{&bad}
		if exactDeny(rows, r, good.Principal) {
			t.Fatal("allow mistaken for exclusion")
		}
		bad = good
		bad.Operation = sarama.AclOperationRead
		rows[0].Acls = []*sarama.Acl{&bad}
		if exactDeny(rows, r, good.Principal) {
			t.Fatal("one permission mistaken for all")
		}
	}
	t.Run("create-reply-error-and-shape-cannot-be-a-success", func(t *testing.T) {
		denied := &sarama.CreateAclsResponse{AclCreationResponses: []*sarama.AclCreationResponse{{Err: sarama.ErrClusterAuthorizationFailed}}}
		if e := kafkaACLCreateResponseError(denied, nil); !errors.Is(e, sarama.ErrClusterAuthorizationFailed) {
			t.Fatal("decoded broker ACL denial was lost", e)
		}
		good := &sarama.CreateAclsResponse{AclCreationResponses: []*sarama.AclCreationResponse{{Err: sarama.ErrNoError}}}
		if e := kafkaACLCreateResponseError(good, nil); e != nil {
			t.Fatal("single decoded successful ACL reply rejected", e)
		}
		for _, bad := range []*sarama.CreateAclsResponse{nil, {}, {AclCreationResponses: []*sarama.AclCreationResponse{nil}}, {AclCreationResponses: []*sarama.AclCreationResponse{{}, {}}}} {
			if e := kafkaACLCreateResponseError(bad, nil); !errors.Is(e, ErrOwnedEvidence) {
				t.Fatal("missing or ambiguous write response became success", e)
			}
		}
		transport := errors.New("fixture-original-transport-failure")
		if e := kafkaACLCreateResponseError(denied, transport); e != transport {
			t.Fatal("original transport error was replaced", e)
		}
	})
	t.Run("describe-reply-error-and-shape-cannot-replace-exact-deny", func(t *testing.T) {
		resource := kafkaDenyResources()[0]
		principal := "User:owned_legacy_fixture"
		acl := sarama.Acl{Principal: principal, Host: "*", Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny}
		row := &sarama.ResourceAcls{Resource: resource, Acls: []*sarama.Acl{&acl}}
		denied := &sarama.DescribeAclsResponse{Err: sarama.ErrClusterAuthorizationFailed, ResourceAcls: []*sarama.ResourceAcls{row}}
		rows, e := kafkaACLDescribeResponseRows(denied, nil)
		if rows != nil || !errors.Is(e, sarama.ErrClusterAuthorizationFailed) {
			t.Fatal("top-level broker error was laundered by an apparent exact row", e)
		}
		good := &sarama.DescribeAclsResponse{ResourceAcls: []*sarama.ResourceAcls{row}}
		rows, e = kafkaACLDescribeResponseRows(good, nil)
		if e != nil || !exactDeny(rows, resource, principal) {
			t.Fatal("successful decoded exact deny rejected", e)
		}
		rows, e = kafkaACLDescribeResponseRows(&sarama.DescribeAclsResponse{}, nil)
		if e != nil || exactDeny(rows, resource, principal) {
			t.Fatal("empty successful reply replaced actual exact-deny evidence", e)
		}
		for _, bad := range []*sarama.DescribeAclsResponse{nil, {ResourceAcls: []*sarama.ResourceAcls{nil}}} {
			rows, e = kafkaACLDescribeResponseRows(bad, nil)
			if rows != nil || !errors.Is(e, ErrOwnedEvidence) {
				t.Fatal("missing read response became evidence", e)
			}
		}
		transport := errors.New("fixture-original-transport-failure")
		rows, e = kafkaACLDescribeResponseRows(good, transport)
		if rows != nil || e != transport {
			t.Fatal("original read transport error was replaced", e)
		}
	})
}
