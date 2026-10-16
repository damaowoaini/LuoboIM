package adminpreview

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"strings"
	"unicode/utf8"
)

// Legacy compatibility only: the original native protocol uses deterministic
// CBC IV=key[:16], without an authentication tag. Never use this for new formats.
func decryptLegacy(content []byte, key string) ([]byte, error) {
	if len(key) != 32 || len(content) == 0 || len(content) > 1024*1024 || len(content)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypted content unavailable")
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("encrypted content unavailable")
	}
	plain := make([]byte, len(content))
	cipher.NewCBCDecrypter(block, []byte(key)[:aes.BlockSize]).CryptBlocks(plain, content)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) || !bytes.Equal(plain[len(plain)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return nil, fmt.Errorf("encrypted content unavailable")
	}
	plain = plain[:len(plain)-padding]
	if !utf8.Valid(plain) || !json.Valid(plain) {
		return nil, fmt.Errorf("encrypted content unavailable")
	}
	return plain, nil
}
func messageItem(message *sdkws.MsgData, content []byte, state string) map[string]any {
	out := map[string]any{"clientMsgID": message.ClientMsgID, "serverMsgID": message.ServerMsgID, "sendID": message.SendID, "recvID": message.RecvID, "groupID": message.GroupID, "senderNickname": message.SenderNickname, "senderFaceURL": message.SenderFaceURL, "senderPlatformID": message.SenderPlatformID, "sessionType": message.SessionType, "msgFrom": message.MsgFrom, "contentType": message.ContentType, "seq": message.Seq, "sendTime": message.SendTime, "createTime": message.CreateTime, "status": message.Status, "isRead": message.IsRead, "attachedInfo": message.AttachedInfo, "ex": message.Ex, "keyVersion": message.KeyVersion, "encrypted": message.KeyVersion > 0, "decryptionState": state}
	if state == "unavailable" || state == "invalid" {
		out["content"] = ""
		return out
	}
	out["content"] = string(content)
	var element any
	if json.Unmarshal(content, &element) != nil {
		return out
	}
	elements := map[int32]string{constant.Text: "textElem", constant.Picture: "pictureElem", constant.Voice: "soundElem", constant.Video: "videoElem", constant.File: "fileElem", constant.AtText: "atTextElem", constant.Merger: "mergeElem", constant.Card: "cardElem", constant.Location: "locationElem", constant.Custom: "customElem", constant.Quote: "quoteElem", constant.AdvancedText: "advancedTextElem"}
	if name := elements[message.ContentType]; name != "" {
		out[name] = element
	} else if message.ContentType >= constant.NotificationBegin {
		out["notificationElem"] = element
	}
	return out
}
func searchable(content []byte) string {
	var value any
	if json.Unmarshal(content, &value) != nil {
		return ""
	}
	stringsFound := []string{}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				visit(item)
			}
		case map[string]any:
			for key, item := range value {
				switch item := item.(type) {
				case string:
					if key == "content" || key == "text" || key == "fileName" || key == "description" || key == "desc" || key == "title" || key == "data" {
						stringsFound = append(stringsFound, item)
					}
				default:
					visit(item)
				}
			}
		}
	}
	visit(value)
	return strings.ToLower(strings.Join(stringsFound, "\n"))
}
