package encryption

import (
	"crypto/rand"
	"encoding/base64"
	"io"
)

// Legacy SDKs use the key directly as 32 UTF-8 bytes, so preserve that wire
// format. 24 CSPRNG bytes encoded without padding provide 192 bits of entropy.
// Existing Redis key/version records are read unchanged and never regenerated.
func generateAESKey() (string, error) { return generateAESKeyFrom(rand.Reader) }
func generateAESKeyFrom(source io.Reader) (string, error) {
	random := make([]byte, 24)
	if _, err := io.ReadFull(source, random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}
