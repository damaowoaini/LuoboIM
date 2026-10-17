//go:build linux

package runtimeacceptance

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// SourceMinio is a separately reviewed source-build input, not a fictional
// official image digest. It is accepted only in an explicitly selected fresh
// Redis-base fixture; the server's1555 source identity remains independent.
type SourceMinio struct {
	Version            string `json:"version"`
	Path               string `json:"path"`
	SHA256             string `json:"SHA256"`
	Bytes              int64  `json:"bytes"`
	ReceiptPath        string `json:"receiptPath"`
	ReceiptSHA256      string `json:"receiptSHA256"`
	Repository         string `json:"repository"`
	Commit             string `json:"commit"`
	SourceDigestSHA256 string `json:"sourceDigestSHA256"`
	GoModSHA256        string `json:"goModSHA256"`
	GoSumSHA256        string `json:"goSumSHA256"`
	ToolchainSHA256    string `json:"toolchainSHA256"`
	CGOEnabled         string `json:"cgoEnabled"`
}

func (m SourceMinio) Validate() error {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if m.Version != "owned-minio-source-elf-v1" || m.Repository != "https://github.com/minio/minio" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(m.Commit) || m.CGOEnabled != "0" || m.Bytes < 1 || m.Bytes > 256<<20 {
		return ownedcontrol.ErrOwnedEvidence
	}
	for _, s := range []string{m.SHA256, m.ReceiptSHA256, m.SourceDigestSHA256, m.GoModSHA256, m.GoSumSHA256, m.ToolchainSHA256} {
		if !hex64.MatchString(s) {
			return ownedcontrol.ErrOwnedEvidence
		}
	}
	if !filepath.IsAbs(m.Path) || filepath.Clean(m.Path) != m.Path || !filepath.IsAbs(m.ReceiptPath) || filepath.Clean(m.ReceiptPath) != m.ReceiptPath {
		return ownedcontrol.ErrOwnedEvidence
	}
	real, e := filepath.EvalSymlinks(m.Path)
	if e != nil || real != m.Path {
		return ownedcontrol.ErrOwnedEvidence
	}
	before, e := os.Lstat(m.Path)
	if e != nil || !before.Mode().IsRegular() || before.Size() != m.Bytes || before.Mode().Perm()&0111 == 0 {
		return ownedcontrol.ErrOwnedEvidence
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
		return ownedcontrol.ErrOwnedEvidence
	}
	f, e := os.OpenFile(m.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return ownedcontrol.ErrOwnedEvidence
	}
	raw, e := io.ReadAll(io.LimitReader(f, m.Bytes+1))
	after, e2 := f.Stat()
	current, e3 := os.Lstat(m.Path)
	if e != nil || e2 != nil || e3 != nil || len(raw) != int(m.Bytes) || digestBytes(raw) != m.SHA256 || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || len(raw) < 20 || !bytes.Equal(raw[:4], []byte{0x7f, 'E', 'L', 'F'}) || raw[4] != 2 || raw[5] != 1 || raw[18] != 62 || raw[19] != 0 {
		return ownedcontrol.ErrOwnedEvidence
	}
	if e = f.Close(); e != nil {
		return e
	}
	receipt, e := namespaceinventory.ReadPrivateDocument(m.ReceiptPath, 64<<10)
	if e != nil || digestBytes(receipt) != m.ReceiptSHA256 {
		return ownedcontrol.ErrOwnedEvidence
	}
	var proof struct {
		Version            string `json:"version"`
		Repository         string `json:"repository"`
		Commit             string `json:"commit"`
		SourceDigestSHA256 string `json:"sourceDigestSHA256"`
		GoModSHA256        string `json:"goModSHA256"`
		GoSumSHA256        string `json:"goSumSHA256"`
		ToolchainSHA256    string `json:"toolchainSHA256"`
		CGOEnabled         string `json:"cgoEnabled"`
		BinarySHA256       string `json:"binarySHA256"`
		BinaryBytes        int64  `json:"binaryBytes"`
	}
	if namespaceinventory.DecodeStrict(receipt, &proof) != nil || proof.Version != "owned-minio-source-build-receipt-v1" || proof.Repository != m.Repository || proof.Commit != m.Commit || proof.SourceDigestSHA256 != m.SourceDigestSHA256 || proof.GoModSHA256 != m.GoModSHA256 || proof.GoSumSHA256 != m.GoSumSHA256 || proof.ToolchainSHA256 != m.ToolchainSHA256 || proof.CGOEnabled != m.CGOEnabled || proof.BinarySHA256 != m.SHA256 || proof.BinaryBytes != m.Bytes {
		return ownedcontrol.ErrOwnedEvidence
	}
	canonical, e := json.Marshal(proof)
	if e != nil || !bytes.Equal(append(canonical, '\n'), receipt) {
		return ownedcontrol.ErrOwnedEvidence
	}
	return nil
}

// The Docker Path selector alone is insufficient: bind the running source
// role's actual executable inode back to the reviewed read-only host ELF.
func (m SourceMinio) ValidateProcess(pid int) error {
	if pid <= 1 {
		return ownedcontrol.ErrOwnedEvidence
	}
	if e := m.Validate(); e != nil {
		return e
	}
	proc := "/proc/" + strconv.Itoa(pid) + "/exe"
	actual, e := os.Stat(proc)
	expected, e2 := os.Stat(m.Path)
	if e != nil || e2 != nil || !os.SameFile(actual, expected) || actual.Size() != m.Bytes {
		return ownedcontrol.ErrOwnedEvidence
	}
	f, e := os.Open(proc)
	if e != nil {
		return e
	}
	raw, e := io.ReadAll(io.LimitReader(f, m.Bytes+1))
	closeErr := f.Close()
	after, e2 := os.Stat(proc)
	if e != nil || closeErr != nil || e2 != nil || !os.SameFile(actual, after) || len(raw) != int(m.Bytes) || digestBytes(raw) != m.SHA256 {
		return ownedcontrol.ErrOwnedEvidence
	}
	return nil
}
