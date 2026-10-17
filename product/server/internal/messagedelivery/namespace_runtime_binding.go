package messagedelivery

import (
	"context"
	"os"
)

type namespaceRuntimeStoreBinding interface {
	validateNamespaceRuntimeStore(context.Context, *Store) error
	checkNamespaceRuntimePolicyCatalog(context.Context, *Store) error
}

// Denial only. The actual owned required startup rejects missing/half private
// runtime tickets before constructing resources. Raw default Stores may never
// turn a process's existing DataReady marker into an owned writer capability.
// Original unowned/legacy/default compatibility is kept; no env value grants.
func namespaceRuntimeBindingRequired() bool {
	return os.Getenv(HistoryWindowModeEnv) == "required" && (os.Getenv("LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH") != "" || os.Getenv("LUOBO_MANAGED_OWNER_NONCE") != "")
}
func (s *Store) checkRequiredPolicyCatalog(ctx context.Context) error {
	if s == nil {
		return ErrUnprepared
	}
	if s.runtimeBinding != nil {
		return s.runtimeBinding.checkNamespaceRuntimePolicyCatalog(ctx, s)
	}
	return s.CheckKafkaReady(ctx)
}
