package messagedelivery

import "context"

// The owned offline runtime has its own Store instance. It is never shared
// with a second offline worker. A restarted runtime creates a new instance;
// old device attempts still name the drained original instance.
func (s *Store) KafkaOfflineInstanceID() string {
	if s == nil {
		return ""
	}
	return s.instanceID
}

// Read every pending ordinary-v1 policy before starting this managed worker.
// Missing adapters, corrupt ordinary duties and uncut legacy Seq0 are unready states,
// never silently skipped or routed through a current replacement policy.
// Other producer families require their separately declared runtime worker.
// This is not a global producer-readiness claim. No DDL/capacity limit exists.
func (s *Store) RequiredKafkaOfflinePolicies(ctx context.Context) ([]string, error) {
	return s.requiredKafkaPolicies(ctx, "offline_pending")
}
