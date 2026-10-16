package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

// A file-descriptor-bound root prevents parent path replacement from redirecting
// the actual create. Every write also proves that the original directory and
// exact output inode remain reachable at their expected paths. An incomplete
// journal remains in place; it is never truncated, removed or resumed.
type privateJournal struct {
	file       *os.File
	directory  *os.File
	root       *os.Root
	parentPath string
	parent     os.FileInfo
	identity   os.FileInfo
	name       string
	closed     bool
}

func newPrivateJournal(path string) (*privateJournal, error) {
	if privateParent(path) != nil {
		return nil, errInput
	}
	parentPath := filepath.Dir(path)
	parent, err := os.Lstat(parentPath)
	if err != nil {
		return nil, errInput
	}
	directory, err := os.OpenFile(parentPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errInput
	}
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(parent, opened) {
		_ = directory.Close()
		return nil, errInput
	}
	root, err := os.OpenRoot(parentPath)
	if err != nil {
		_ = directory.Close()
		return nil, errInput
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, rootInfo) {
		_ = root.Close()
		_ = directory.Close()
		return nil, errInput
	}
	name := filepath.Base(path)
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		_ = root.Close()
		_ = directory.Close()
		return nil, errInput
	}
	j := &privateJournal{file: file, directory: directory, root: root, parentPath: parentPath, parent: parent, name: name}
	j.identity, err = file.Stat()
	if err != nil || j.current() != nil || file.Sync() != nil || directory.Sync() != nil {
		_ = j.Close()
		return nil, errInput
	}
	return j, nil
}
func (j *privateJournal) current() error {
	if j == nil || j.closed || j.file == nil || j.identity == nil {
		return errInput
	}
	parent, err := os.Lstat(j.parentPath)
	if err != nil || privateParent(filepath.Join(j.parentPath, j.name)) != nil || !os.SameFile(j.parent, parent) {
		return errInput
	}
	opened, err := j.file.Stat()
	if err != nil {
		return errInput
	}
	current, err := j.root.Lstat(j.name)
	if err != nil || !os.SameFile(j.identity, opened) || !os.SameFile(j.identity, current) || !current.Mode().IsRegular() || current.Mode().Perm() != 0o600 {
		return errInput
	}
	owner, ok := current.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return errInput
	}
	return nil
}
func (j *privateJournal) Write(value any) error {
	if j.current() != nil || json.NewEncoder(j.file).Encode(value) != nil || j.file.Sync() != nil || j.directory.Sync() != nil || j.current() != nil {
		return errInput
	}
	return nil
}
func (j *privateJournal) Close() error {
	if j == nil || j.closed {
		return errInput
	}
	failed := j.current() != nil
	if j.file.Sync() != nil {
		failed = true
	}
	if j.directory.Sync() != nil {
		failed = true
	}
	if j.file.Close() != nil {
		failed = true
	}
	if j.root.Close() != nil {
		failed = true
	}
	if j.directory.Close() != nil {
		failed = true
	}
	j.closed = true
	if failed {
		return errInput
	}
	return nil
}
