// Package adminbootstrap reads explicit initial credentials only for an empty
// administrator table. It never derives a default from an account identifier.
package adminbootstrap

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const FileEnv = "LUOBO_ADMIN_BOOTSTRAP_PASSWORDS_FILE"

func Load(accounts []string) (map[string]string, error) {
	file, err := os.Open(os.Getenv(FileEnv))
	if err != nil {
		return nil, fmt.Errorf("empty admin database requires a private %s JSON file", FileEnv)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return nil, fmt.Errorf("bootstrap credential file must be regular, private (0600), and at most64KiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return nil, fmt.Errorf("bootstrap credential file cannot be read")
	}
	var passwords map[string]string
	if json.Unmarshal(data, &passwords) != nil {
		return nil, fmt.Errorf("bootstrap credential file must be an account-to-password JSON object")
	}
	result := make(map[string]string, len(accounts))
	for _, account := range accounts {
		password, ok := passwords[account]
		if account == "" || !ok || len(password) < 20 || len(password) > 256 || strings.EqualFold(password, account) {
			return nil, fmt.Errorf("each configured initial administrator requires an explicit20..256-byte password distinct from the account")
		}
		digest := md5.Sum([]byte(password))
		result[account] = hex.EncodeToString(digest[:])
	}
	return result, nil
}
