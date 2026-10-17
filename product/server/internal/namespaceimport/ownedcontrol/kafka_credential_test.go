//go:build linux

package ownedcontrol

import (
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
}
