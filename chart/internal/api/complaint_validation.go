package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func bindComplaint(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(target); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉参数格式无效"))
		return false
	}
	return true
}

func complaintText(value string, maxBytes int, required, multiline bool) bool {
	if !utf8.ValidString(value) || len(value) > maxBytes || (required && strings.TrimSpace(value) == "") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}

func complaintID(value string, required bool) bool {
	return complaintText(value, 64, required, false) && value == strings.TrimSpace(value)
}

func complaintSubmissionID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func complaintPayloadHash(kind int32, target, name, reason, description string, evidence []string) string {
	if evidence == nil {
		evidence = []string{}
	}
	encoded, _ := json.Marshal(struct {
		Kind                              int32
		Target, Name, Reason, Description string
		Evidence                          []string
	}{kind, target, name, reason, description, evidence})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func complaintInput(kind int32, target, name, reason, description string, evidence []string) bool {
	if (kind != 1 && kind != 2) || !complaintID(target, true) ||
		!complaintText(name, 255, false, false) || !complaintText(reason, 500, true, false) ||
		!complaintText(description, 4096, false, true) || len(evidence) > 9 {
		return false
	}
	seen := make(map[string]bool, len(evidence))
	for _, image := range evidence {
		if len(image) > 2048 || seen[image] || !complaintText(image, 2048, true, false) {
			return false
		}
		parsed, err := url.Parse(image)
		if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return false
		}
		seen[image] = true
	}
	return true
}

func complaintPage(page, size int32, kind, status *int32) bool {
	return page >= 1 && page <= 100000 && size >= 1 && size <= 100 &&
		(kind == nil || *kind == 1 || *kind == 2) &&
		(status == nil || *status >= 0 && *status <= 2)
}
