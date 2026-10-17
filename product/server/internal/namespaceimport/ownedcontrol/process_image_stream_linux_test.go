//go:build linux

package ownedcontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A per-call Context boundary coordinates real file/process changes. It does
// not replace procfs, hashing, Wait, Signal or any production package variable.
type imageBoundaryContext struct {
	context.Context
	ready  func() bool
	action func()
	once   sync.Once
}

func (c *imageBoundaryContext) Err() error {
	if c.ready() {
		c.once.Do(c.action)
	}
	return c.Context.Err()
}

func TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation(t *testing.T) {
	t.Run("actual-original-image-and-bounded-file-pins", func(t *testing.T) {
		binary, e := os.Executable()
		if e != nil {
			t.Fatal("original test executable unavailable")
		}
		binary, e = filepath.EvalSymlinks(binary)
		if e != nil {
			t.Fatal("original executable identity unavailable")
		}
		f, e := os.Open(binary)
		if e != nil {
			t.Fatal("original executable read unavailable")
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal("original executable pin unavailable")
		}
		pin := hex.EncodeToString(h.Sum(nil))
		if e = actualImage(os.Getpid(), binary, pin); e != nil {
			t.Fatal("fresh original image rejected")
		}
		if actualImage(os.Getpid(), binary, strings.Repeat("0", 64)) == nil || actualImage(os.Getpid(), binary+"-foreign", pin) == nil {
			t.Fatal("wrong body or foreign executable path acquired image authority")
		}
		for _, size := range []int64{0, 256*1024*1024 + 1} {
			path := filepath.Join(t.TempDir(), "bounded-image")
			f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if e != nil {
				t.Fatal("bounded image fixture unavailable")
			}
			if e = f.Truncate(size); e != nil {
				f.Close()
				t.Fatal("bounded image fixture size unavailable")
			}
			e = verifyImageFile(context.Background(), f, path, pin)
			f.Close()
			if e == nil {
				t.Fatal("empty or oversized image was accepted")
			}
		}
	})
	t.Run("real-fd-path-stability-and-mid-read-cancellation", func(t *testing.T) {
		data := bytes.Repeat([]byte{0x5a}, 256*1024)
		for _, kind := range []string{"stable", "path-replaced", "metadata-changed", "cancelled"} {
			path := filepath.Join(t.TempDir(), "image")
			if os.WriteFile(path, data, 0600) != nil {
				t.Fatal("image fixture unavailable")
			}
			f, e := os.Open(path)
			if e != nil {
				t.Fatal("original image FD unavailable")
			}
			t.Cleanup(func() { _ = f.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			changed := false
			gate := &imageBoundaryContext{Context: ctx, ready: func() bool {
				at, e := f.Seek(0, io.SeekCurrent)
				if e != nil {
					t.Fatal("original image cursor unavailable")
				}
				return at > 0
			}, action: func() {
				changed = true
				switch kind {
				case "path-replaced":
					if os.Rename(path, path+"-original") != nil || os.WriteFile(path, data, 0600) != nil {
						t.Fatal("real image replacement unavailable")
					}
				case "metadata-changed":
					if os.Chtimes(path, time.Unix(1700000000, 0), time.Unix(1700000001, 0)) != nil {
						t.Fatal("real image metadata change unavailable")
					}
				case "cancelled":
					cancel()
				}
			}}
			e = verifyImageFile(gate, f, path, sha(data))
			at, seekErr := f.Seek(0, io.SeekCurrent)
			f.Close()
			cancel()
			if !changed || seekErr != nil {
				t.Fatal("test did not reach a real image read boundary")
			}
			if kind == "stable" && e != nil {
				t.Fatal("stable original FD and path rejected")
			}
			if kind != "stable" && e == nil {
				t.Fatal("changed or cancelled image acquired authority")
			}
			if kind == "cancelled" && (!errors.Is(e, context.Canceled) || at >= int64(len(data))) {
				t.Fatal("cancellation did not stop a partial real image read")
			}
		}
	})
}
