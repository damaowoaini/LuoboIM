package convert

import (
	"bytes"
	"github.com/OpenIMSDK/protocol/sdkws"
	"testing"
)

func TestMessageConversionPreservesOpaqueCiphertextAndVersion(t *testing.T) {
	original := &sdkws.MsgData{SendID: "u1", RecvID: "u2", Content: []byte{0, 255, 1, 128, 42}, KeyVersion: 17, Seq: 8}
	stored := MsgPb2DB(original)
	restored := MsgDB2Pb(stored)
	if stored.KeyVersion != 17 || restored.KeyVersion != 17 || !bytes.Equal(restored.Content, original.Content) {
		t.Fatal("opaque encryption content/version lost in conversion")
	}
}
