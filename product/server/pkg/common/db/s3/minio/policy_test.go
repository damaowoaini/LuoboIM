package minio

import (
	"encoding/json"
	"testing"
)

func TestPublicReadPolicyHasOnlyGetObject(t *testing.T) {
	var policy struct {
		Version   string
		Statement []struct {
			Action    []string
			Effect    string
			Principal struct{ AWS []string }
			Resource  []string
		}
	}
	if err := json.Unmarshal([]byte(publicReadPolicy("synthetic-read-only")), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Version != "2012-10-17" || len(policy.Statement) != 1 {
		t.Fatal("unexpected policy shape")
	}
	s := policy.Statement[0]
	if len(s.Action) != 1 || s.Action[0] != "s3:GetObject" || s.Effect != "Allow" || len(s.Principal.AWS) != 1 || s.Principal.AWS[0] != "*" || len(s.Resource) != 1 || s.Resource[0] != "arn:aws:s3:::synthetic-read-only/*" {
		t.Fatal("anonymous write or expanded policy")
	}
}
