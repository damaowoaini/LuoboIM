package cont

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"strings"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
)

func (c *Controller) resolveUpload(ctx context.Context, id string, rebuildLegacy bool) (*multipartUploadID, time.Time, error) {
	if strings.HasPrefix(id, intent.Prefix) {
		var upload multipartUploadID
		expires, err := c.intents.Load(ctx, id, "multipart", c.Engine(), &upload)
		if err != nil {
			return nil, time.Time{}, err
		}
		if err := c.validateUpload(&upload); err != nil {
			return nil, time.Time{}, err
		}
		if err := c.validateScope(ctx, &upload); err != nil {
			return nil, time.Time{}, err
		}
		return &upload, expires, nil
	}
	// Legacy input proves no ownership. Source key and provider ID are never used.
	old, err := parseMultipartUploadID(id)
	if err != nil || old.Type != UploadTypePresigned || old.ID != "" {
		return nil, time.Time{}, errs.ErrNoPermission.Wrap("legacy multipart upload requires a new authenticated intent")
	}
	var recovered multipartUploadID
	expires, err := c.intents.LoadLegacy(ctx, id, c.Engine(), &recovered)
	if err == nil {
		if err := c.validateUpload(&recovered); err != nil {
			return nil, time.Time{}, err
		}
		return &recovered, expires, c.validateScope(ctx, &recovered)
	}
	if !rebuildLegacy {
		return nil, time.Time{}, err
	}
	// This is a fresh upload for untrusted size/hash, not recovery of the old key.
	if old.Size <= 0 {
		return nil, time.Time{}, errs.ErrArgs.Wrap("invalid upload size")
	}
	hashBytes, err := hex.DecodeString(old.Hash)
	if err != nil || len(hashBytes) != md5.Size {
		return nil, time.Time{}, errs.ErrArgs.Wrap("invalid upload hash")
	}
	partSize, err := c.impl.PartSize(ctx, old.Size)
	if err != nil {
		return nil, time.Time{}, err
	}
	if old.Size > partSize {
		return nil, time.Time{}, errs.ErrArgs.Wrap("invalid legacy single-part size")
	}
	scope, err := c.intents.Scope(ctx, c.Engine())
	if err != nil {
		return nil, time.Time{}, err
	}
	fresh := multipartUploadID{Type: UploadTypePresigned, Key: tempPath + c.NowPath() + "/" + c.UUID() + ".recovered.presigned", Size: old.Size, Hash: strings.ToLower(old.Hash), PartSize: partSize, Scope: scope.ID}
	expires, err = c.intents.RecoverLegacy(ctx, id, c.Engine(), &fresh, intent.MaxLifetime, &recovered)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := c.validateUpload(&recovered); err != nil {
		return nil, time.Time{}, err
	}
	return &recovered, expires, c.validateScope(ctx, &recovered)
}

func (c *Controller) validateScope(ctx context.Context, upload *multipartUploadID) error {
	scope, err := c.intents.Scope(ctx, c.Engine())
	if err != nil {
		return err
	}
	if !strings.HasPrefix(upload.Key, tempPath) || strings.Contains(upload.Key, "..") {
		return errs.ErrNoPermission.Wrap("upload intent requires a new attachment upload")
	}
	if upload.Scope == "" && upload.Type == UploadTypePresigned {
		// Previously issued owner-bound presigned capabilities reference only a
		// random temporary key. Require fresh byte validation before v2 proof.
		return nil
	}
	if upload.Scope != scope.ID {
		// Old multipart capabilities may point at a shared global hash key.
		// They must not be signed or completed after this upgrade.
		return errs.ErrNoPermission.Wrap("upload intent requires a new attachment upload")
	}
	return nil
}
func (c *Controller) validateUpload(upload *multipartUploadID) error {
	if upload.Size <= 0 || upload.PartSize <= 0 || upload.Key == "" {
		return errs.ErrArgs.Wrap("invalid stored upload intent")
	}
	hash, err := hex.DecodeString(upload.Hash)
	if err != nil || len(hash) != md5.Size {
		return errs.ErrArgs.Wrap("invalid stored upload hash")
	}
	if upload.Type == UploadTypePresigned && upload.Size <= upload.PartSize && upload.ID == "" {
		return nil
	}
	if upload.Type == UploadTypeMultipart && upload.ID != "" {
		return nil
	}
	return errs.ErrArgs.Wrap("invalid stored upload type")
}
func numberOfParts(upload *multipartUploadID) int { return int((upload.Size-1)/upload.PartSize + 1) }
func validatePartNumbers(upload *multipartUploadID, numbers []int) error {
	count := numberOfParts(upload)
	if len(numbers) == 0 || len(numbers) > count {
		return errs.ErrArgs.Wrap("invalid upload part list")
	}
	seen := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		if number < 1 || number > count || seen[number] {
			return errs.ErrArgs.Wrap("invalid upload part number")
		}
		seen[number] = true
	}
	if upload.Type == UploadTypePresigned && (len(numbers) != 1 || numbers[0] != 1) {
		return errs.ErrArgs.Wrap("single-part upload requires only part1")
	}
	return nil
}
func validateCompletedParts(upload *multipartUploadID, hashes []string) error {
	if len(hashes) > 10000 || len(hashes) != numberOfParts(upload) {
		return errs.ErrArgs.Wrap("upload part count mismatching")
	}
	for _, part := range hashes {
		raw, err := hex.DecodeString(part)
		if err != nil || len(raw) != md5.Size {
			return errs.ErrArgs.Wrap("invalid upload part hash")
		}
	}
	return nil
}
