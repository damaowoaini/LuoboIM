// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cont

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"

	"github.com/google/uuid"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
)

type uploadStore interface {
	Create(context.Context, string, string, any, time.Duration) (string, error)
	Load(context.Context, string, string, string, any) (time.Time, error)
	RecoverLegacy(context.Context, string, string, any, time.Duration, any) (time.Time, error)
	LoadLegacy(context.Context, string, string, any) (time.Time, error)
	Scope(context.Context, string) (intent.Scope, error)
	FindCompleted(context.Context, string, string, int64) (*intent.CompletionProof, error)
	CommitCompleted(context.Context, string, string, int64, string, string, []string) (*intent.CompletionProof, error)
}

func New(cache cache.S3Cache, impl s3.Interface, intents uploadStore) *Controller {
	return &Controller{
		cache:   cache,
		impl:    impl,
		intents: intents,
	}
}

type Controller struct {
	cache   cache.S3Cache
	impl    s3.Interface
	intents uploadStore
}

func (c *Controller) Engine() string {
	return c.impl.Engine()
}

func (c *Controller) NowPath() string {
	now := time.Now()

	return path.Join(
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()),
		fmt.Sprintf("%02d", now.Hour()),
		fmt.Sprintf("%02d", now.Minute()),
		fmt.Sprintf("%02d", now.Second()),
	)
}

func (c *Controller) UUID() string {
	id := uuid.New()

	return hex.EncodeToString(id[:])
}

func (c *Controller) PartSize(ctx context.Context, size int64) (int64, error) {
	return c.impl.PartSize(ctx, size)
}

func (c *Controller) PartLimit() *s3.PartLimit {
	return c.impl.PartLimit()
}

func (c *Controller) StatObject(ctx context.Context, name string) (*s3.ObjectInfo, error) {
	return c.cache.GetKey(ctx, c.impl.Engine(), name)
}

func (c *Controller) GetHashObject(ctx context.Context, hash string, size int64) (*s3.ObjectInfo, error) {
	proof, err := c.intents.FindCompleted(ctx, c.Engine(), hash, size)
	if err != nil {
		return nil, err
	}
	return c.statCompleted(ctx, proof)
}

func (c *Controller) VerifyCompletion(ctx context.Context, hash string, size int64, key string) (string, error) {
	proof, err := c.intents.FindCompleted(ctx, c.Engine(), hash, size)
	if err != nil {
		return "", err
	}
	if proof.Key != key {
		return "", errs.ErrNoPermission.Wrap("object completion proof required")
	}
	if _, err := c.statCompleted(ctx, proof); err != nil {
		return "", err
	}
	return proof.ID, nil
}

func (c *Controller) statCompleted(ctx context.Context, proof *intent.CompletionProof) (*s3.ObjectInfo, error) {
	// Cache entries and client aliases are not possession evidence. Revalidate
	// the exact immutable provider object referenced by the permanent proof.
	info, err := c.impl.StatObject(ctx, proof.Key)
	if err != nil {
		return nil, errs.ErrData.Wrap("completed object unavailable; operator repair required")
	}
	if info.Key != proof.Key || info.Size != proof.Size || info.ETag != proof.ETag {
		return nil, errs.ErrData.Wrap("completed object proof does not match storage")
	}
	return info, nil
}

func (c *Controller) InitiateUpload(ctx context.Context, hash string, size int64, expire time.Duration, maxParts int) (*InitiateUploadResult, error) {
	defer log.ZDebug(ctx, "return")
	if size <= 0 || expire <= 0 || expire > intent.MaxLifetime {
		return nil, errors.New("invalid size")
	}
	if hashBytes, err := hex.DecodeString(hash); err != nil {
		return nil, err
	} else if len(hashBytes) != md5.Size {
		return nil, errors.New("invalid md5")
	}
	hash = strings.ToLower(hash)
	scope, err := c.intents.Scope(ctx, c.Engine())
	if err != nil {
		return nil, err
	}
	partSize, err := c.impl.PartSize(ctx, size)
	if err != nil {
		return nil, err
	}
	if partSize <= 0 {
		return nil, errs.ErrInternalServer.Wrap("invalid storage part size")
	}
	partNumber := int(size / partSize)
	if size%partSize > 0 {
		partNumber++
	}
	if maxParts > partNumber {
		maxParts = partNumber
	}
	if info, err := c.GetHashObject(ctx, hash, size); err == nil {
		return nil, &HashAlreadyExistsError{Object: info}
	} else if !errs.ErrRecordNotFound.Is(err) {
		return nil, err
	}
	if size <= partSize {
		// 预签名上传
		key := path.Join(tempPath, c.NowPath(), fmt.Sprintf("%s_%d_%s.presigned", hash, size, c.UUID()))
		uploadID, err := c.intents.Create(ctx, "multipart", c.Engine(), multipartUploadID{Type: UploadTypePresigned, Key: key, Size: size, Hash: hash, PartSize: partSize, Scope: scope.ID}, expire)
		if err != nil {
			return nil, err
		}
		rawURL, err := c.impl.PresignedPutObject(ctx, key, expire)
		if err != nil {
			return nil, err
		}
		return &InitiateUploadResult{
			UploadID: uploadID,
			PartSize: partSize,
			Sign: &s3.AuthSignResult{
				Parts: []s3.SignPart{
					{
						PartNumber: 1,
						URL:        rawURL,
					},
				},
			},
		}, nil
	} else {
		// 分片上传
		// Multipart bytes also stay in an unpredictable temporary object until
		// completion is validated. A known hash never selects a writable key.
		key := path.Join(tempPath, c.NowPath(), c.UUID()+".multipart")
		upload, err := c.impl.InitiateMultipartUpload(ctx, key)
		if err != nil {
			return nil, err
		}
		if upload.Key != key || upload.UploadID == "" {
			return nil, errs.ErrData.Wrap("storage returned an invalid upload target")
		}
		uploadID, err := c.intents.Create(ctx, "multipart", c.Engine(), multipartUploadID{Type: UploadTypeMultipart, ID: upload.UploadID, Key: key, Size: size, Hash: hash, PartSize: partSize, Scope: scope.ID}, expire)
		if err != nil {
			_ = c.impl.AbortMultipartUpload(ctx, upload.UploadID, upload.Key)
			return nil, err
		}
		if maxParts < 0 {
			maxParts = partNumber
		}
		var authSign *s3.AuthSignResult
		if maxParts > 0 {
			partNumbers := make([]int, maxParts)
			for i := 0; i < maxParts; i++ {
				partNumbers[i] = i + 1
			}
			authSign, err = c.impl.AuthSign(ctx, upload.UploadID, upload.Key, min(expire, time.Hour*24), partNumbers)
			if err != nil {
				return nil, err
			}
		}
		return &InitiateUploadResult{
			UploadID: uploadID,
			PartSize: partSize,
			Sign:     authSign,
		}, nil
	}
}

func (c *Controller) CompleteUpload(ctx context.Context, uploadID string, partHashs []string) (*UploadResult, error) {
	defer log.ZDebug(ctx, "return")
	upload, _, err := c.resolveUpload(ctx, uploadID, false)
	if err != nil {
		return nil, err
	}
	if err := validateCompletedParts(upload, partHashs); err != nil {
		return nil, err
	}
	if md5Sum := md5.Sum([]byte(strings.Join(partHashs, partSeparator))); hex.EncodeToString(md5Sum[:]) != upload.Hash {
		return nil, errors.New("md5 mismatching")
	}
	if info, err := c.GetHashObject(ctx, upload.Hash, upload.Size); err == nil {
		return &UploadResult{
			Key:  info.Key,
			Size: info.Size,
			Hash: upload.Hash,
		}, nil
	} else if !errs.ErrRecordNotFound.Is(err) {
		return nil, err
	}
	scope, err := c.intents.Scope(ctx, c.Engine())
	if err != nil {
		return nil, err
	}
	cleanObject := make(map[string]struct{})
	committed := false
	defer func() {
		if !committed {
			// A provider/database error may have an unknown ACK. Retain the
			// temporary bytes and any new permanent copy for an exact retry.
			return
		}
		for key := range cleanObject {
			_ = c.impl.DeleteObject(ctx, key)
		}
	}()
	var uploaded *s3.ObjectInfo
	switch upload.Type {
	case UploadTypeMultipart:
		parts := make([]s3.Part, len(partHashs))
		for i, part := range partHashs {
			parts[i] = s3.Part{
				PartNumber: i + 1,
				ETag:       part,
			}
		}
		result, err := c.impl.CompleteMultipartUpload(ctx, upload.ID, upload.Key, parts)
		if err != nil {
			// Completing a provider upload can commit before its ACK is lost.
			// Only the scoped random temp key with the exact standard multipart
			// ETag and size can recover that case; no global-key fallback exists.
			info, statErr := c.impl.StatObject(ctx, upload.Key)
			if statErr != nil || info.Key != upload.Key || info.Size != upload.Size || info.ETag != multipartETag(partHashs) {
				return nil, err
			}
			uploaded = info
		} else {
			if result.Key != upload.Key {
				return nil, errs.ErrData.Wrap("storage completed an invalid upload target")
			}
			info, err := c.impl.StatObject(ctx, result.Key)
			if err != nil {
				return nil, err
			}
			if info.Key != upload.Key || info.Size != upload.Size || info.ETag == "" || info.ETag != normalizeETag(result.ETag) {
				return nil, errs.ErrArgs.Wrap("upload size mismatching")
			}
			uploaded = info
		}
		// The provider verifies each submitted part ETag before completion.
		// The existing wire hash is MD5(join(part ETags)), not a raw-file hash.
		cleanObject[uploaded.Key] = struct{}{}
	case UploadTypePresigned:
		uploadInfo, err := c.impl.StatObject(ctx, upload.Key)
		if err != nil {
			return nil, err
		}
		cleanObject[uploadInfo.Key] = struct{}{}
		if uploadInfo.Key != upload.Key || uploadInfo.Size != upload.Size {
			return nil, errors.New("upload size mismatching")
		}
		md5Sum := md5.Sum([]byte(strings.Join([]string{uploadInfo.ETag}, partSeparator)))
		if md5val := hex.EncodeToString(md5Sum[:]); md5val != upload.Hash {
			return nil, errs.ErrArgs.Wrap(fmt.Sprintf("md5 mismatching %s != %s", md5val, upload.Hash))
		}
		uploaded = uploadInfo
	default:
		return nil, errors.New("invalid upload id type")
	}
	// The source may remain writable until its capability expires. Copy to a
	// random immutable destination, then stat that destination before recording
	// proof. Concurrent completions never overwrite an existing permanent key.
	keyID, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	targetKey := scope.ObjectPrefix(upload.Hash, upload.Size) + keyID.String()
	copied, err := c.impl.CopyObject(ctx, uploaded.Key, targetKey)
	if err != nil {
		return nil, err
	}
	if copied.Key != targetKey || copied.ETag == "" || (upload.Type == UploadTypePresigned && normalizeETag(copied.ETag) != uploaded.ETag) {
		return nil, errs.ErrData.Wrap("completed object copy mismatching")
	}
	finalInfo, err := c.impl.StatObject(ctx, targetKey)
	if err != nil {
		return nil, err
	}
	if finalInfo.Key != targetKey || finalInfo.Size != upload.Size || finalInfo.ETag != normalizeETag(copied.ETag) {
		return nil, errs.ErrData.Wrap("completed object stat mismatching")
	}
	proof, err := c.intents.CommitCompleted(ctx, c.Engine(), upload.Hash, upload.Size, targetKey, finalInfo.ETag, partHashs)
	if err != nil {
		return nil, err
	}
	// Another upload from this same proven scope can win the first insert.
	// Revalidate the winning proof; never replace it with this attempt's key.
	if _, err := c.statCompleted(ctx, proof); err != nil {
		return nil, err
	}
	committed = true
	return &UploadResult{
		Key:  proof.Key,
		Size: upload.Size,
		Hash: upload.Hash,
	}, nil
}

func normalizeETag(value string) string { return strings.ToLower(strings.Trim(value, `"`)) }

func multipartETag(parts []string) string {
	digest := md5.New()
	for _, part := range parts {
		decoded, _ := hex.DecodeString(part) // validateCompletedParts ran first.
		_, _ = digest.Write(decoded)
	}
	return hex.EncodeToString(digest.Sum(nil)) + "-" + fmt.Sprint(len(parts))
}

func (c *Controller) AuthSign(ctx context.Context, uploadID string, partNumbers []int) (*s3.AuthSignResult, error) {
	if !strings.HasPrefix(uploadID, intent.Prefix) && (len(partNumbers) != 1 || partNumbers[0] != 1) {
		return nil, errs.ErrArgs.Wrap("legacy single-part retry requires only part1")
	}
	upload, expires, err := c.resolveUpload(ctx, uploadID, true)
	if err != nil {
		return nil, err
	}
	if err := validatePartNumbers(upload, partNumbers); err != nil {
		return nil, err
	}
	duration := time.Until(expires)
	if duration <= 0 {
		return nil, errs.ErrNoPermission.Wrap("upload intent expired")
	}
	if duration > 24*time.Hour {
		duration = 24 * time.Hour
	}
	switch upload.Type {
	case UploadTypeMultipart:
		return c.impl.AuthSign(ctx, upload.ID, upload.Key, duration, partNumbers)
	case UploadTypePresigned:
		rawURL, err := c.impl.PresignedPutObject(ctx, upload.Key, duration)
		if err != nil {
			return nil, err
		}
		return &s3.AuthSignResult{Parts: []s3.SignPart{{PartNumber: 1, URL: rawURL}}}, nil
	default:
		return nil, errs.ErrArgs.Wrap("invalid stored upload type")
	}
}

func (c *Controller) IsNotFound(err error) bool {
	return c.impl.IsNotFound(err) || errs.ErrRecordNotFound.Is(err)
}

func (c *Controller) AccessURL(ctx context.Context, name string, expire time.Duration, opt *s3.AccessURLOption) (string, error) {
	if opt.Image != nil {
		opt.Filename = ""
		opt.ContentType = ""
	}

	return c.impl.AccessURL(ctx, name, expire, opt)
}

func (c *Controller) FormData(ctx context.Context, name string, size int64, contentType string, duration time.Duration) (*s3.FormData, error) {
	return c.impl.FormData(ctx, name, size, contentType, duration)
}
