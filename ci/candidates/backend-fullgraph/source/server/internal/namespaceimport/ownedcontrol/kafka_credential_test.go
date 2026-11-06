//go:build linux

package ownedcontrol

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"testing"
	"time"
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
	t.Run("new-deny-visibility-is-bounded-without-grace-for-existing-denies", func(t *testing.T) {
		resource := kafkaDenyResources()[2]
		principal := "User:owned_legacy_fixture"
		acl := sarama.Acl{Principal: principal, Host: "*", Operation: sarama.AclOperationAll, PermissionType: sarama.AclPermissionDeny}
		good := []sarama.ResourceAcls{{Resource: resource, Acls: []*sarama.Acl{&acl}}}
		wrongACL := acl
		wrongACL.Principal = "User:another_fixture"
		wrong := []sarama.ResourceAcls{{Resource: resource, Acls: []*sarama.Acl{&wrongACL}}}
		valid := func() error { return nil }
		reads := 0
		e := confirmKafkaDeny(context.Background(), resource, principal, true, valid, func() ([]sarama.ResourceAcls, error) {
			reads++
			switch reads {
			case 1:
				return nil, nil
			case 2:
				return wrong, nil
			default:
				return good, nil
			}
		})
		if e != nil || reads != 3 {
			t.Fatal("new deny did not wait for its exact original principal", e, reads)
		}
		for _, rows := range [][]sarama.ResourceAcls{nil, wrong, good} {
			reads = 0
			e = confirmKafkaDeny(context.Background(), resource, principal, false, valid, func() ([]sarama.ResourceAcls, error) { reads++; return rows, nil })
			if reads != 1 || (e == nil) != exactDeny(rows, resource, principal) {
				t.Fatal("existing deny acquired a grace period or a missing row became evidence", e, reads)
			}
		}
		for _, original := range []error{sarama.ErrClusterAuthorizationFailed, ErrOwnedEvidence, errors.New("original-transport-failure")} {
			reads = 0
			e = confirmKafkaDeny(context.Background(), resource, principal, true, valid, func() ([]sarama.ResourceAcls, error) { reads++; return good, original })
			if e != original || reads != 1 {
				t.Fatal("original broker, shape or transport error was retried or replaced", e, reads)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		reads = 0
		e = confirmKafkaDeny(ctx, resource, principal, true, valid, func() ([]sarama.ResourceAcls, error) { reads++; return nil, nil })
		cancel()
		if !errors.Is(e, context.DeadlineExceeded) || reads > 1 {
			t.Fatal("missing new deny ignored the caller deadline", e, reads)
		}
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		reads = 0
		e = confirmKafkaDeny(ctx, resource, principal, true, valid, func() ([]sarama.ResourceAcls, error) { reads++; return good, nil })
		if !errors.Is(e, context.Canceled) || reads != 0 {
			t.Fatal("cancelled confirmation issued a broker read", e, reads)
		}
		ctx, cancel = context.WithCancel(context.Background())
		e = confirmKafkaDeny(ctx, resource, principal, true, valid, func() ([]sarama.ResourceAcls, error) { cancel(); return good, nil })
		if !errors.Is(e, context.Canceled) {
			t.Fatal("a row returned after cancellation became confirmation", e)
		}
		lost := false
		e = confirmKafkaDeny(context.Background(), resource, principal, true, func() error {
			if lost {
				return ErrOwnedEvidence
			}
			return nil
		}, func() ([]sarama.ResourceAcls, error) { lost = true; return good, nil })
		if !errors.Is(e, ErrOwnedEvidence) {
			t.Fatal("a row returned after hold loss became confirmation", e)
		}
		reads = 0
		e = confirmKafkaDeny(context.Background(), resource, principal, true, func() error { return ErrOwnedEvidence }, func() ([]sarama.ResourceAcls, error) { reads++; return good, nil })
		if !errors.Is(e, ErrOwnedEvidence) || reads != 0 {
			t.Fatal("invalid hold issued a confirmation read", e, reads)
		}
		if e = confirmKafkaDeny(nil, resource, principal, true, valid, nil); !errors.Is(e, ErrOwnedEvidence) {
			t.Fatal("invalid confirmation inputs accepted", e)
		}
	})
}
