package namespaceinventory

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
)

// Only the exact synchronous StepRoots invocation receives this private
// capability. It contains no parsed objects, raw bytes or authority grant.
// Ordinary callers still perform every original fresh check.
type workspaceReadScopeKey struct{}
type workspaceReadScope struct {
	workspace                *ImportWorkspace
	snapshot                 *ImportSnapshot
	source                   PageSource
	dir, rawDir, fingerprint string
	active                   atomic.Bool
}

func sameReadScopeSource(a, b PageSource) bool {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	return x.IsValid() && y.IsValid() && x.Kind() == reflect.Pointer && y.Kind() == reflect.Pointer && !x.IsNil() && !y.IsNil() && x.Type() == y.Type() && x.Pointer() == y.Pointer()
}
func (s *workspaceReadScope) matches(w *ImportWorkspace) bool {
	return s != nil && w != nil && w.snapshot != nil && s.workspace == w && s.snapshot == w.snapshot && s.dir == w.dir && s.rawDir == w.snapshot.dir && s.fingerprint == w.snapshot.Fingerprint() && sameReadScopeSource(s.source, w.live)
}
func checkWorkspaceReadScope(ctx context.Context, w *ImportWorkspace) (bool, error) {
	if ctx == nil {
		return false, nil
	}
	value := ctx.Value(workspaceReadScopeKey{})
	if value == nil {
		return false, nil
	}
	s, ok := value.(*workspaceReadScope)
	if !ok || s == nil || !s.active.Load() || !s.matches(w) {
		return true, ErrInvalid
	}
	return true, ctx.Err()
}

// WithSnapshotReadScope shares only the sealed snapshot metadata validation
// within one synchronous StepRoots call. Every private object is still freshly
// read/hash/strict decoded. Commit uses checkFresh at BOTH original boundaries,
// and ImportSnapshot.ReadReference retains its own two fresh checks.
// The context must not escape or be forked by the callback. A retained context
// is rejected after return; no cached capability survives an invocation.
func (w *ImportWorkspace) WithSnapshotReadScope(ctx context.Context, read func(context.Context) error) (resultErr error) {
	if ctx == nil || read == nil || ctx.Value(workspaceReadScopeKey{}) != nil || w == nil || w.snapshot == nil || !sameReadScopeSource(w.live, w.live) {
		return ErrInvalid
	}
	if e := w.checkFresh(ctx); e != nil {
		return e
	}
	s := &workspaceReadScope{workspace: w, snapshot: w.snapshot, source: w.live, dir: w.dir, rawDir: w.snapshot.dir, fingerprint: w.snapshot.Fingerprint()}
	s.active.Store(true)
	defer func() {
		s.active.Store(false)
		// Retire before revalidation, including all error/panic exits. A final
		// failure cannot promote the callback result to a validated checkpoint.
		endErr := w.checkFresh(ctx)
		if !s.matches(w) {
			endErr = errors.Join(endErr, ErrInvalid)
		}
		if endErr != nil {
			if resultErr == nil {
				resultErr = endErr
			} else {
				resultErr = errors.Join(resultErr, endErr)
			}
		}
	}()
	return read(context.WithValue(ctx, workspaceReadScopeKey{}, s))
}
