package messagedelivery

import (
	"encoding/json"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// The same sorted, odd-node-promoting tree as namespaceimport.SortedSetRoot,
// maintained in O(log N) memory. The private DB cursor supplies unique sorted
// actual values; the persisted frontier permits bounded transactions/resume.
type importSetFrontier struct {
	Count  int64      `json:"count"`
	Levels [63]string `json:"levels"`
}

func importSetHash(values ...string) string { body, _ := json.Marshal(values); return hash(body) }

func (f importSetFrontier) valid() bool {
	if f.Count < 0 {
		return false
	}
	for level, value := range f.Levels {
		occupied := uint64(f.Count)&(uint64(1)<<level) != 0
		if occupied && !validSHA(value) || !occupied && value != "" {
			return false
		}
	}
	return true
}

func (f *importSetFrontier) append(value string) error {
	if f == nil || !f.valid() || !validSHA(value) || f.Count >= math.MaxInt64-1 {
		return ErrInvalid
	}
	current := importSetHash("namespace-import-set-leaf-v1", value)
	for level := range f.Levels {
		if f.Levels[level] == "" {
			f.Levels[level] = current
			f.Count++
			return nil
		}
		current = importSetHash("namespace-import-set-node-v1", f.Levels[level], current)
		f.Levels[level] = ""
	}
	return ErrInvalid
}

func (f importSetFrontier) root() (string, error) {
	if !f.valid() {
		return "", ErrInvalid
	}
	if f.Count == 0 {
		return namespaceimport.SortedSetRoot(nil)
	}
	value := ""
	for _, left := range f.Levels {
		if left == "" {
			continue
		}
		if value == "" {
			value = left
		} else {
			value = importSetHash("namespace-import-set-node-v1", left, value)
		}
	}
	if !validSHA(value) {
		return "", ErrConflict
	}
	return value, nil
}
