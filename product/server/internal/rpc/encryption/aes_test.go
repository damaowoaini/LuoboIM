package encryption

import (
	"crypto/aes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

type brokenRandom struct{}

func (brokenRandom) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
func TestAESKeyUsesCSPRNGAndPreservesLegacyWireLength(t *testing.T) {
	if key, err := generateAESKeyFrom(brokenRandom{}); err == nil || key != "" {
		t.Fatal("entropy failure silently generated a key")
	}
	if _, err := generateAESKeyFrom(io.LimitReader(strings.NewReader("short"), 5)); err == nil {
		t.Fatal("partial entropy accepted")
	}
	var wg sync.WaitGroup
	keys := make(chan string, 256)
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := generateAESKey()
			if err != nil {
				t.Error(err)
				return
			}
			keys <- key
		}()
	}
	wg.Wait()
	close(keys)
	seen := map[string]bool{}
	for key := range keys {
		if len(key) != 32 || seen[key] {
			t.Fatal("key length/uniqueness invalid")
		}
		if _, err := aes.NewCipher([]byte(key)); err != nil {
			t.Fatal("legacy AES consumer rejected key")
		}
		seen[key] = true
	}
}
