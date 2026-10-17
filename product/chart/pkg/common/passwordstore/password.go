// Package passwordstore accepts the existing client's MD5 wire credential, and
// stores a salted Argon2id verifier. No client protocol change is required.
package passwordstore

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const memory, iterations, parallelism, saltLength, keyLength = 64 * 1024, 3, 4, 16, 32

// Bound authentication memory usage to 256 MiB per process. Requests waiting
// for capacity can be cancelled before allocating Argon2 working memory.
var capacity = make(chan struct{}, 4)

func wireCredential(wire string) (string, error) {
	decoded, err := hex.DecodeString(wire)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("invalid MD5 wire credential")
	}
	return strings.ToLower(wire), nil
}

func derive(ctx context.Context, wire string, salt []byte, timeCost, memoryCost uint32, lanes uint8) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case capacity <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-capacity }()
	return argon2.IDKey([]byte(wire), salt, timeCost, memoryCost, lanes, keyLength), nil
}

func Hash(ctx context.Context, wire string) (string, error) {
	wire, err := wireCredential(wire)
	if err != nil {
		return "", err
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := derive(ctx, wire, salt, iterations, memory, parallelism)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, iterations, parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether the supplied credential matches, and whether an
// authenticated legacy digest needs a compare-and-swap format upgrade.
func Verify(ctx context.Context, stored, wire string) (matches, legacy bool, err error) {
	wire, err = wireCredential(wire)
	if err != nil {
		return false, false, nil
	}
	if !strings.HasPrefix(stored, "$argon2id$") {
		old, err := wireCredential(stored)
		if err != nil {
			return false, false, nil
		}
		ok := subtle.ConstantTimeCompare([]byte(old), []byte(wire)) == 1
		return ok, ok, nil
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, false, fmt.Errorf("unsupported password verifier")
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, false, fmt.Errorf("malformed password parameters")
	}
	values := make([]uint64, 3)
	for i, prefix := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], prefix) {
			return false, false, fmt.Errorf("malformed password parameters")
		}
		value, e := strconv.ParseUint(strings.TrimPrefix(params[i], prefix), 10, 32)
		if e != nil {
			return false, false, fmt.Errorf("malformed password parameters")
		}
		values[i] = value
	}
	// Refuse untrusted allocation/CPU parameters before deriving a key.
	if values[0] < 8*values[2] || values[0] > memory || values[1] < 1 || values[1] > iterations || values[2] < 1 || values[2] > parallelism {
		return false, false, fmt.Errorf("password verifier parameters exceed policy")
	}
	salt, e1 := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	key, e2 := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) != saltLength || len(key) != keyLength {
		return false, false, fmt.Errorf("malformed password verifier")
	}
	actual, err := derive(ctx, wire, salt, uint32(values[1]), uint32(values[0]), uint8(values[2]))
	if err != nil {
		return false, false, err
	}
	return subtle.ConstantTimeCompare(actual, key) == 1, false, nil
}
