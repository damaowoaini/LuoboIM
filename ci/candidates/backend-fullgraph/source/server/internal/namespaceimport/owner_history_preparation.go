package namespaceimport

import "context"

// A private source-integrity optimization boundary, never a write/read grant.
// Capture and Snapshot must be retained nonnil pointers to the ACTUAL objects;
// a resumed materializer or a reopened snapshot is a different preparation.
// MetadataSHA256 binds immutable snapshot/session/checkpoint, complete sealed
// lookup roots, policy, header and held lease/guard. Derived progress may advance.
type OriginalHistoryPreparationBinding struct {
	Capture         any
	Snapshot        any
	SnapshotSHA256  string
	RawRootSHA256   string
	HeaderSHA256    string
	LeaseID         string
	HeldGuardSHA256 string
	MetadataSHA256  string
}
type OriginalHistoryPreparedReader interface {
	OriginalHistoryPreparationBinding(context.Context) (OriginalHistoryPreparationBinding, error)
	RecheckOriginalHistoryPreparedRefs(context.Context, OriginalHistoryPreparationBinding, []SourceRef) error
}
