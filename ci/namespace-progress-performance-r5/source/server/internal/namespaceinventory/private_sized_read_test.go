package namespaceinventory

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type privateReadStep struct {
	data []byte
	err  error
}

type scriptedPrivateRead struct {
	steps []privateReadStep
	bytes int
	reads int
}

func (r *scriptedPrivateRead) Read(p []byte) (int, error) {
	r.reads++
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	step := &r.steps[0]
	n := copy(p, step.data)
	r.bytes += n
	step.data = step.data[n:]
	if len(step.data) != 0 {
		return n, nil
	}
	err := step.err
	r.steps = r.steps[1:]
	return n, err
}

// Exact original read block, retained as the allocation/IO-result baseline.
// The surrounding private-path/stat checks remain in ReadPrivateDocument.
func oldPrivateReadBlock(reader io.Reader, size int64, maximum int) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, int64(maximum)+1))
	if err != nil || int64(len(raw)) != size {
		return nil, ErrPrivate
	}
	return raw, nil
}

func TestInventorySizedReadPreservesLengthAndReadErrors(t *testing.T) {
	failure := errors.New("controlled read failure")
	cases := []struct {
		name    string
		steps   []privateReadStep
		maximum int
		bad     bool
	}{
		{"exact-and-later-EOF", []privateReadStep{{data: []byte("page")}}, 8, false},
		{"short-chunks", []privateReadStep{{data: []byte("pa")}, {data: []byte("ge")}}, 8, false},
		{"EOF-with-final-bytes", []privateReadStep{{data: []byte("page"), err: io.EOF}}, 8, false},
		{"nonEOF-with-final-bytes", []privateReadStep{{data: []byte("page"), err: failure}}, 8, true},
		{"wrappedEOF-with-final-bytes", []privateReadStep{{data: []byte("page"), err: errors.Join(io.EOF)}}, 8, true},
		{"nonEOF-after-exact-bytes", []privateReadStep{{data: []byte("page")}, {err: failure}}, 8, true},
		{"shrunk-input", []privateReadStep{{data: []byte("pag")}}, 8, true},
		{"grown-input", []privateReadStep{{data: []byte("pages")}}, 8, true},
		{"zero-progress", []privateReadStep{{}}, 8, true},
		{"admitted-maximum", []privateReadStep{{data: []byte("page")}}, 4, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := func() *scriptedPrivateRead {
				return &scriptedPrivateRead{steps: append([]privateReadStep(nil), tc.steps...)}
			}
			current, original := fresh(), fresh()
			got, err := readPrivateSized(current, 4, tc.maximum)
			old, oldErr := oldPrivateReadBlock(original, 4, tc.maximum)
			if (err != nil) != tc.bad || (oldErr != nil) != tc.bad {
				t.Fatal("current or original IO-result contract differs", err, oldErr)
			}
			if tc.bad {
				if !errors.Is(err, ErrPrivate) || got != nil {
					t.Fatal("failed read exposed partial evidence", err)
				}
			} else if !bytes.Equal(got, []byte("page")) || !bytes.Equal(got, old) {
				t.Fatal("admitted exact bytes changed")
			}
			if current.bytes > tc.maximum+1 || original.bytes > tc.maximum+1 || current.reads > 4 {
				t.Fatal("bounded read did not stop on its original length/error/EOF boundary")
			}
		})
	}
}

func TestInventoryPrivateSizedFileReadAvoidsChunkAllocations(t *testing.T) {
	// The encoded member-page size observed in the unchanged Large fixture.
	want := bytes.Repeat([]byte("x"), 54034)
	path := filepath.Join(privateFixtureDir(t), "sized-read.json")
	writeFixture(t, path, want)
	got, err := ReadPrivateDocument(path, len(want))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("real private FD round trip changed", err)
	}
	if _, err := ReadPrivateDocument(path, len(want)-1); !errors.Is(err, ErrPrivate) {
		t.Fatal("original maximum admitted a larger private file", err)
	}
	measure := func(read func(io.Reader, int64, int) ([]byte, error)) float64 {
		return testing.AllocsPerRun(3, func() {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal("allocation comparison could not open the same private file", err)
			}
			info, statErr := file.Stat()
			if statErr != nil || info.Size() != int64(len(want)) {
				_ = file.Close()
				t.Fatal("allocation comparison length changed", statErr)
			}
			body, readErr := read(file, info.Size(), len(want))
			closeErr := file.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(body, want) {
				t.Fatal("allocation comparison changed its IO result", readErr, closeErr)
			}
		})
	}
	original := measure(oldPrivateReadBlock)
	current := measure(readPrivateSized)
	if current >= original {
		t.Fatalf("checked-length read did not reduce original fragment allocations: new=%g old=%g", current, original)
	}
}
