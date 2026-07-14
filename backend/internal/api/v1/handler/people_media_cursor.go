package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/gin-gonic/gin"
)

// Cursor version for forward compatibility. If the payload structure changes,
// bump this and reject older versions with INVALID_CURSOR.
const cursorVersion = 1

const (
	cursorKindPhotos = "photos"
	cursorKindFaces  = "faces"
)

// cursorPayload is the internal structure encoded into the opaque cursor string.
// It contains only sort-field values and id — no file paths, names, or PII.
type cursorPayload struct {
	Version      int     `json:"v"`
	Kind         string  `json:"k"`
	TakenAt      *int64  `json:"t,omitempty"`  // unix millis, photos only (nil = NULL zone)
	ID           uint    `json:"i"`            // last item id from previous page
	QualityScore float64 `json:"q,omitempty"`  // faces only
}

// encodeCursor serializes a cursorPayload into a URL-safe base64 string.
func encodeCursor(p cursorPayload) string {
	raw, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor decodes and validates an opaque cursor string.
// kind must be cursorKindPhotos or cursorKindFaces.
// Returns INVALID_CURSOR error for malformed, wrong-version, or kind-mismatched cursors.
func decodeCursor(raw, kind string) (*cursorPayload, error) {
	if raw == "" {
		return nil, nil // first page, no cursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("INVALID_CURSOR: malformed base64")
	}
	var p cursorPayload
	if err := json.Unmarshal(decoded, &p); err != nil {
		return nil, fmt.Errorf("INVALID_CURSOR: malformed json")
	}
	if p.Version != cursorVersion {
		return nil, fmt.Errorf("INVALID_CURSOR: unsupported version %d", p.Version)
	}
	if p.Kind != kind {
		return nil, fmt.Errorf("INVALID_CURSOR: cursor kind %q does not match expected %q", p.Kind, kind)
	}
	return &p, nil
}

// writeCursorError writes a 400 response with a stable error code.
func writeCursorError(c *gin.Context, err error) {
	code := "INVALID_CURSOR"
	if msg := err.Error(); len(msg) >= len("INVALID_CURSOR") && msg[:len("INVALID_CURSOR")] == "INVALID_CURSOR" {
		// keep the prefix as the code, full message as detail
	}
	c.JSON(http.StatusBadRequest, model.Response{
		Success: false,
		Error: &model.ErrorInfo{
			Code:    code,
			Message: err.Error(),
		},
	})
}

// timeToMillis converts a *time.Time to *int64 (unix millis).
func timeToMillis(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

// millisToTime converts *int64 (unix millis) to *time.Time.
func millisToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms)
	return &t
}
