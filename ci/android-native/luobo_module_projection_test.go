package main

// This is the only added Go source in an owned copy of the fixed official
// x/mobile commit. It calls the real helper; it does not generate binding source.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLuoboProjectAndroidModule(t *testing.T) {
	checkedDir := func(key string) string {
		t.Helper()
		value := os.Getenv(key)
		resolved, err := filepath.EvalSymlinks(value)
		if err != nil || !filepath.IsAbs(value) || resolved != value {
			t.Fatalf("%s must be an existing canonical absolute directory", key)
		}
		info, err := os.Lstat(value)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s is not a regular directory", key)
		}
		return value
	}
	core, output := checkedDir("LUOBO_PROJECTION_CORE"), checkedDir("LUOBO_PROJECTION_OUT")
	if core == output || strings.HasPrefix(output, core+string(os.PathSeparator)) || strings.HasPrefix(core, output+string(os.PathSeparator)) {
		t.Fatal("source and output must be separate owned directories")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatal("projection output must be new and empty")
	}
	before := map[string][]byte{}
	for _, name := range []string{"go.mod", "go.sum"} {
		body, err := os.ReadFile(filepath.Join(core, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = body
	}
	sum := sha256.Sum256(before["go.sum"])
	if hex.EncodeToString(sum[:]) != "8eb9b73313a7f774bfef48814bfc1b73e7ff7f57bd37f9d40555e8d24f70262f" {
		t.Fatal("reviewed core acquired lock is required")
	}
	oldTags := buildTags
	buildTags = stringsFlag{"luobo_fixture_ca_debug"}
	defer func() { buildTags = oldTags }()
	t.Chdir(core)
	// getModuleVersions sets GOOS=android/GOARCH=arm64 for its real go-list.
	// ensureGoVersion intentionally projects Go 1.27 (minor), not 1.27.2.
	if err := writeGoMod(output, "android", "arm64"); err != nil {
		t.Fatal(err)
	}
	for name, wanted := range before {
		after, err := os.ReadFile(filepath.Join(core, name))
		if err != nil || !bytes.Equal(after, wanted) {
			t.Fatalf("core %s changed during projection", name)
		}
	}
	path := filepath.Join(output, "go.mod")
	projected, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(projected, []byte("module gobind\n")) || !bytes.Contains(projected, []byte("\ngo 1.27\n")) {
		t.Fatal("real helper did not produce the expected module and Go-minor directive")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(projected)
	proof, err := json.MarshalIndent(map[string]any{
		"source": "official writeGoMod -> getModuleVersions -> parseModuleVersions -> Format",
		"target_GOOS": "android", "target_GOARCH": "arm64",
		"target_tags": []string{"luobo_fixture_ca_debug", "android"},
		"host_GOOS": os.Getenv("GOOS"), "host_GOARCH": os.Getenv("GOARCH"),
		"CGO_ENABLED": os.Getenv("CGO_ENABLED"), "GOFLAGS": os.Getenv("GOFLAGS"),
		"projected_go_mod_sha256": hex.EncodeToString(digest[:]),
		"generated_Go_or_Java_files": 0, "empty_module_tidy": false, "full_bind_lock": false,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(output, "projection.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(proof, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
}
