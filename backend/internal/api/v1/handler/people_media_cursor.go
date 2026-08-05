package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/gin-gonic/gin"
)

// Cursor version for forward compatibility. If the payload structure changes,
// bump this and reject older versions with INVALID_CURSOR.
const cursorVersion = 1

const (
	cursorKindPhotos    = "photos"
	cursorKindFaces     = "faces"
	cursorKindPhotoList = "photo_list" // 照片管理页连续浏览
)

// cursorPayload is the internal structure encoded into the opaque cursor string.
// It contains only sort-field values and id — no file paths, names, or PII.
type cursorPayload struct {
	Version      int     `json:"v"`
	Kind         string  `json:"k"`
	TakenAt      *int64  `json:"t,omitempty"` // unix millis, photos only (nil = NULL zone)
	ID           uint    `json:"i"`           // last item id from previous page
	QualityScore float64 `json:"q,omitempty"` // faces only
}

// encodeCursor serializes a cursorPayload into a URL-safe base64 string.
func encodeCursor(p cursorPayload) string {
	raw, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor decodes and validates an opaque cursor string.
// kind must be one of cursorKindPhotos / cursorKindFaces / cursorKindPhotoList.
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
//
// 必须 UTC：cursor 保存的是 Unix 毫秒（绝对时刻，与时区无关），但 keyset 查询拿它和
// SQLite 的 taken_at（GORM 写入时按 Location 序列化文本）做大小比较。若解码出的 time 带
// Local 时区，GORM 会按 Local 格式化成带偏移的字符串，与库内 UTC 文本比较时可能因排序边界
// （如 DST 切换、跨日）错位，导致 cursor 不推进、重复返回同一页。强制 UTC 保证 round-trip
// 与库内存储文本一致。
func millisToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}

// assertCursorAdvanced 校验 hasMore=true 时 nextCursor 相对输入 cursor 严格推进。
// 排序为 (taken_at DESC, id DESC)，NULL taken_at 排在非空之后。
// 任一不满足返回 PAGINATION_STALLED，handler 应停止向客户端返回该 cursor。
//
// 规则：
//   - nextCursor 必须非 nil 且 ID != 0；
//   - nextCursor 不能等于输入 cursor（同 taken_at + 同 id → 停滞）；
//   - 排序值严格落在输入 cursor 之后。
//
// 输入 cursor 为 nil（首页）时只要 nextCursor 合法即可。
func assertCursorAdvanced(prev *repository.PersonPhotoCursor, next *repository.PersonPhotoCursor) error {
	if next == nil {
		return fmt.Errorf("PAGINATION_STALLED: hasMore=true but nextCursor is nil")
	}
	if next.ID == 0 {
		return fmt.Errorf("PAGINATION_STALLED: nextCursor has zero id")
	}
	if prev == nil {
		return nil // 首页，无前驱，nextCursor 合法即放行
	}
	// 停滞：同 taken_at 且同 id。
	if cursorEqual(prev, next) {
		return fmt.Errorf("PAGINATION_STALLED: nextCursor equals input cursor (taken_at=%v id=%d)", prev.TakenAt, prev.ID)
	}
	// 排序推进校验：(taken_at DESC, id DESC) → next 必须严格“更小”。
	if !cursorStrictlyAfter(prev, next) {
		return fmt.Errorf("PAGINATION_STALLED: nextCursor does not advance past input cursor")
	}
	return nil
}

// cursorEqual 比较两个 cursor 的 (taken_at, id) 是否完全相同（nil taken_at 视为相等区）。
func cursorEqual(a, b *repository.PersonPhotoCursor) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID {
		return false
	}
	if (a.TakenAt == nil) != (b.TakenAt == nil) {
		return false
	}
	if a.TakenAt == nil {
		return true
	}
	return a.TakenAt.Equal(*b.TakenAt)
}

// cursorStrictlyAfter 判断 next 是否在 (taken_at DESC, id DESC) 序下严格落在 prev 之后。
// 即 next 的排序键 < prev 的排序键（更靠后）。NULL taken_at 排在所有非空之后。
func cursorStrictlyAfter(prev, next *repository.PersonPhotoCursor) bool {
	if prev == nil {
		return true
	}
	// prev 非空、next NULL → next 在 NULL 区，排序更靠后。
	if prev.TakenAt != nil && next.TakenAt == nil {
		return true
	}
	// prev NULL、next 非空 → next 反而更靠前，回退。
	if prev.TakenAt == nil && next.TakenAt != nil {
		return false
	}
	// 两者均 NULL：按 id DESC，next.id 必须 < prev.id。
	if prev.TakenAt == nil {
		return next.ID < prev.ID
	}
	// 两者均非空：taken_at 更小 → 更靠后；taken_at 相等 → id 必须更小。
	if next.TakenAt.Before(*prev.TakenAt) {
		return true
	}
	if next.TakenAt.After(*prev.TakenAt) {
		return false
	}
	// taken_at 相等
	return next.ID < prev.ID
}
