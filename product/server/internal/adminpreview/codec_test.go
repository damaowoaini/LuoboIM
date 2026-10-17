package adminpreview

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func legacyVector(t *testing.T) ([]byte, string, []byte) {
	t.Helper()
	data, err := os.ReadFile("testdata/legacy_encryption_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Plaintext string `json:"plaintextUtf8"`
		Captures  []struct {
			Content string `json:"contentBase64"`
			Key     string `json:"key"`
		} `json:"captures"`
	}
	if json.Unmarshal(data, &fixture) != nil || len(fixture.Captures) == 0 {
		t.Fatal("synthetic native vector invalid")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(fixture.Captures[0].Content)
	if err != nil {
		t.Fatal(err)
	}
	return ciphertext, fixture.Captures[0].Key, []byte(fixture.Plaintext)
}
func TestLegacyCBCNativeVectorAndMalformedCiphertext(t *testing.T) {
	ciphertext, key, expected := legacyVector(t)
	plain, err := decryptLegacy(ciphertext, key)
	if err != nil || !bytes.Equal(plain, expected) {
		t.Fatal("original native CBC compatibility vector failed")
	}
	invalid := append([]byte{}, ciphertext...)
	invalid[len(invalid)-1] ^= 255
	for _, input := range [][]byte{nil, {}, ciphertext[:len(ciphertext)-1], make([]byte, 16), invalid} {
		if _, err := decryptLegacy(input, key); err == nil {
			t.Fatal("malformed/padding-invalid ciphertext accepted")
		}
	}
	if _, err := decryptLegacy(ciphertext, "short"); err == nil {
		t.Fatal("invalid key length accepted")
	}
	if searchable(plain) != "synthetic encryption fixture" {
		t.Fatal("keyword extraction searched JSON field names")
	}
}
