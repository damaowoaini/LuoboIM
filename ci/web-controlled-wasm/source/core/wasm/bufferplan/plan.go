// Package bufferplan validates the existing seven-argument Web buffer ABI.
// It does not create paths, change UUIDs, upload, or dispatch a message.
package bufferplan

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/openimsdk/openim-sdk-core/v3/pkg/constant"
	"github.com/openimsdk/openim-sdk-core/v3/sdk_struct"
)

var ErrPlan = errors.New("invalid buffer message or media metadata")

type File struct {
	UUID string `json:"uuid"`
	Size int64  `json:"size"`
	Slot int    `json:"slot"`
}

type Plan struct {
	Message sdk_struct.MsgStruct
	JSON    string
	Files   []File
}

func ID(value string) bool {
	return value != "" && len(value) <= 1024 && strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, func(c rune) bool { return unicode.IsControl(c) })
}

func UUID(value string) bool {
	// ByFile appends '/'+File.name; it remains an opaque map key, not a path.
	return value != "" && len(value) <= 1024 && strings.TrimSpace(value) != "" &&
		!strings.ContainsFunc(value, func(c rune) bool { return unicode.IsControl(c) })
}

// A negative length means no ArrayBuffer was supplied. Zero is a present empty
// ArrayBuffer; generic files retain that pre-existing File/Blob capability.
func Prepare(raw, recv, group string, lengths [2]int64) (Plan, error) {
	p := Plan{JSON: raw}
	if len(raw) == 0 || len(raw) > 4*1024*1024 || (recv == "") == (group == "") ||
		(recv != "" && !ID(recv)) || (group != "" && !ID(group)) {
		return p, ErrPlan
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil || object == nil ||
		json.Unmarshal([]byte(raw), &p.Message) != nil || !ID(p.Message.ClientMsgID) {
		return p, ErrPlan
	}
	m := &p.Message
	if (m.GroupID != "" && m.GroupID != group) || (recv != "" && m.RecvID != "" && m.RecvID != recv) {
		return p, ErrPlan
	}
	changed := false
	add := func(uuid string, declared int64, slot int, field, nested, sizeKey string, emptyFile bool) error {
		actual := lengths[slot]
		if !UUID(uuid) || actual < 0 || actual > 2147483647 || (!emptyFile && actual == 0) ||
			declared < 0 || (declared > 0 && declared != actual) {
			return ErrPlan
		}
		for _, f := range p.Files {
			if f.UUID == uuid {
				return ErrPlan
			}
		}
		p.Files = append(p.Files, File{UUID: uuid, Size: actual, Slot: slot})
		if declared == 0 && actual != 0 {
			var elem map[string]json.RawMessage
			if json.Unmarshal(object[field], &elem) != nil || elem == nil {
				return ErrPlan
			}
			var sizes map[string]json.RawMessage
			if nested != "" {
				if json.Unmarshal(elem[nested], &sizes) != nil || sizes == nil {
					return ErrPlan
				}
			} else {
				sizes = elem
			}
			sizes[sizeKey], _ = json.Marshal(actual)
			if nested != "" {
				elem[nested], _ = json.Marshal(sizes)
			}
			object[field], _ = json.Marshal(elem)
			changed = true
		}
		return nil
	}
	var err error
	// URL-created / already-uploaded messages use the ordinary SDK path. Their
	// provided buffers must never cause another map registration or upload.
	ready := m.Status == constant.MsgStatusSendSuccess
	switch m.ContentType {
	case constant.Picture:
		if m.PictureElem == nil || m.PictureElem.SourcePicture == nil {
			return p, ErrPlan
		}
		if ready {
			if m.PictureElem.SourcePicture.Url == "" {
				return p, ErrPlan
			}
			break
		}
		err = add(m.PictureElem.SourcePicture.UUID, m.PictureElem.SourcePicture.Size, 0, "pictureElem", "sourcePicture", "size", false)
	case constant.Sound:
		if m.SoundElem == nil {
			return p, ErrPlan
		}
		if ready {
			if m.SoundElem.SourceURL == "" {
				return p, ErrPlan
			}
			break
		}
		err = add(m.SoundElem.UUID, m.SoundElem.DataSize, 0, "soundElem", "", "dataSize", false)
	case constant.Video:
		if m.VideoElem == nil {
			return p, ErrPlan
		}
		if ready {
			if m.VideoElem.VideoURL == "" || m.VideoElem.SnapshotURL == "" {
				return p, ErrPlan
			}
			break
		}
		err = add(m.VideoElem.VideoUUID, m.VideoElem.VideoSize, 0, "videoElem", "", "videoSize", false)
		if err == nil {
			err = add(m.VideoElem.SnapshotUUID, m.VideoElem.SnapshotSize, 1, "videoElem", "", "snapshotSize", false)
		}
	case constant.File:
		if m.FileElem == nil {
			return p, ErrPlan
		}
		if ready {
			if m.FileElem.SourceURL == "" {
				return p, ErrPlan
			}
			break
		}
		err = add(m.FileElem.UUID, m.FileElem.FileSize, 0, "fileElem", "", "fileSize", true)
	default:
		if lengths[0] >= 0 || lengths[1] >= 0 {
			return p, ErrPlan
		}
	}
	if err != nil {
		return p, err
	}
	if !ready && m.ContentType != constant.Video && lengths[1] >= 0 {
		return p, ErrPlan
	}
	if changed {
		encoded, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			return p, marshalErr
		}
		p.JSON = string(encoded)
	}
	return p, nil
}

// UUIDs selects only the actual media upload keys for synchronous entry claims.
// It never treats the opaque keys as paths or plans uploads for status2 URLs.
func UUIDs(raw string) (string, []string) {
	var m sdk_struct.MsgStruct
	if len(raw) > 4*1024*1024 || json.Unmarshal([]byte(raw), &m) != nil {
		return "", nil
	}
	if m.Status == constant.MsgStatusSendSuccess {
		return m.ClientMsgID, nil
	}
	var ids []string
	switch m.ContentType {
	case constant.Picture:
		if m.PictureElem != nil && m.PictureElem.SourcePicture != nil {
			ids = append(ids, m.PictureElem.SourcePicture.UUID)
		}
	case constant.Sound:
		if m.SoundElem != nil {
			ids = append(ids, m.SoundElem.UUID)
		}
	case constant.Video:
		if m.VideoElem != nil {
			ids = append(ids, m.VideoElem.VideoUUID, m.VideoElem.SnapshotUUID)
		}
	case constant.File:
		if m.FileElem != nil {
			ids = append(ids, m.FileElem.UUID)
		}
	}
	filtered := ids[:0]
	for _, id := range ids {
		if id != "" {
			filtered = append(filtered, id)
		}
	}
	return m.ClientMsgID, filtered
}
