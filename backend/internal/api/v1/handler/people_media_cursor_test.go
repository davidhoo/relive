package handler

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeCursor_RoundTrip_Photos(t *testing.T) {
	now := time.Now()
	ms := now.UnixMilli()
	original := cursorPayload{
		Version: cursorVersion,
		Kind:    cursorKindPhotos,
		TakenAt: &ms,
		ID:      42,
	}
	encoded := encodeCursor(original)
	decoded, err := decodeCursor(encoded, cursorKindPhotos)
	require.NoError(t, err)
	require.NotNil(t, decoded)
	assert.Equal(t, cursorVersion, decoded.Version)
	assert.Equal(t, cursorKindPhotos, decoded.Kind)
	assert.NotNil(t, decoded.TakenAt)
	assert.Equal(t, ms, *decoded.TakenAt)
	assert.Equal(t, uint(42), decoded.ID)
}

func TestEncodeDecodeCursor_RoundTrip_Faces(t *testing.T) {
	original := cursorPayload{
		Version:      cursorVersion,
		Kind:         cursorKindFaces,
		QualityScore: 0.95,
		ID:           100,
	}
	encoded := encodeCursor(original)
	decoded, err := decodeCursor(encoded, cursorKindFaces)
	require.NoError(t, err)
	require.NotNil(t, decoded)
	assert.Equal(t, cursorKindFaces, decoded.Kind)
	assert.Equal(t, 0.95, decoded.QualityScore)
	assert.Equal(t, uint(100), decoded.ID)
}

func TestEncodeDecodeCursor_NullTakenAt(t *testing.T) {
	original := cursorPayload{
		Version: cursorVersion,
		Kind:    cursorKindPhotos,
		TakenAt: nil, // NULL zone
		ID:      7,
	}
	encoded := encodeCursor(original)
	decoded, err := decodeCursor(encoded, cursorKindPhotos)
	require.NoError(t, err)
	require.NotNil(t, decoded)
	assert.Nil(t, decoded.TakenAt)
	assert.Equal(t, uint(7), decoded.ID)
}

func TestDecodeCursor_EmptyString(t *testing.T) {
	decoded, err := decodeCursor("", cursorKindPhotos)
	require.NoError(t, err)
	assert.Nil(t, decoded)
}

func TestDecodeCursor_KindMismatch(t *testing.T) {
	photosCursor := encodeCursor(cursorPayload{
		Version: cursorVersion,
		Kind:    cursorKindPhotos,
		ID:      1,
	})
	_, err := decodeCursor(photosCursor, cursorKindFaces)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_CURSOR")
}

func TestDecodeCursor_MalformedBase64(t *testing.T) {
	_, err := decodeCursor("!!!not-base64!!!", cursorKindPhotos)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_CURSOR")
}

func TestDecodeCursor_UnknownVersion(t *testing.T) {
	raw, _ := json.Marshal(cursorPayload{Version: 999, Kind: cursorKindPhotos, ID: 1})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	_, err := decodeCursor(encoded, cursorKindPhotos)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_CURSOR")
}

func TestDecodeCursor_MalformedJSON(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("{broken"))
	_, err := decodeCursor(encoded, cursorKindPhotos)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_CURSOR")
}

func TestCursorPayload_NoSensitiveFields(t *testing.T) {
	// Ensure cursor payload struct has no file_path, name, or other PII fields.
	// This is a structural guard: if someone adds a sensitive field, this test
	// should be updated to reflect the policy change.
	p := cursorPayload{Version: 1, Kind: cursorKindPhotos, ID: 1}
	data, _ := json.Marshal(p)
	str := string(data)
	assert.NotContains(t, str, "file_path")
	assert.NotContains(t, str, "file_name")
	assert.NotContains(t, str, "name")
	assert.NotContains(t, str, "thumbnail")
}

func TestTimeMillisRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	ms := timeToMillis(&now)
	require.NotNil(t, ms)
	back := millisToTime(ms)
	require.NotNil(t, back)
	assert.True(t, now.Equal(*back))
}

func TestTimeMillis_NilStaysNil(t *testing.T) {
	assert.Nil(t, timeToMillis(nil))
	assert.Nil(t, millisToTime(nil))
}

// TestMillisToTime_AlwaysUTC 验证无论服务器时区如何，millisToTime 返回的 time 总是 UTC。
// 线上 server 通常 time.Local=Asia/Shanghai，若解码出的 time 带 Local，GORM 会按 +08:00
// 格式化 taken_at，与库内 UTC 文本比较时在排序边界错位，导致 cursor 不推进。
func TestMillisToTime_AlwaysUTC(t *testing.T) {
	// 临时把服务器时区改成 Asia/Shanghai，模拟线上环境。
	orig := time.Local
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	time.Local = loc
	t.Cleanup(func() { time.Local = orig })

	ms := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	back := millisToTime(&ms)
	require.NotNil(t, back)

	// 不只比较时间值相等（time.Equal 跨时区也可能 true），还要断言 Location 就是 UTC。
	assert.Equal(t, time.UTC, back.Location(), "decoded cursor time must be in UTC regardless of server time.Local")
}

// TestAssertCursorAdvanced 校验推进保护逻辑。
func TestAssertCursorAdvanced(t *testing.T) {
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) // 更早 → DESC 更靠后

	t.Run("hasMore but nil next returns error", func(t *testing.T) {
		err := assertCursorAdvanced(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
	t.Run("hasMore but zero id returns error", func(t *testing.T) {
		err := assertCursorAdvanced(nil, &repository.PersonPhotoCursor{TakenAt: &t1, ID: 0})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
	t.Run("first page (prev nil) with valid next passes", func(t *testing.T) {
		err := assertCursorAdvanced(nil, &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10})
		require.NoError(t, err)
	})
	t.Run("equal cursor returns error", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		next := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		err := assertCursorAdvanced(prev, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
	t.Run("earlier taken_at advances (passes)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		next := &repository.PersonPhotoCursor{TakenAt: &t2, ID: 5}
		err := assertCursorAdvanced(prev, next)
		require.NoError(t, err)
	})
	t.Run("same taken_at smaller id advances (passes)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		next := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 9}
		err := assertCursorAdvanced(prev, next)
		require.NoError(t, err)
	})
	t.Run("same taken_at larger id is regression (error)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		next := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 11}
		err := assertCursorAdvanced(prev, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
	t.Run("later taken_at is regression (error)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: &t2, ID: 5}
		next := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		err := assertCursorAdvanced(prev, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
	t.Run("non-null prev to null next advances (passes)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		next := &repository.PersonPhotoCursor{TakenAt: nil, ID: 5}
		err := assertCursorAdvanced(prev, next)
		require.NoError(t, err)
	})
	t.Run("null prev to non-null next is regression (error)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: nil, ID: 5}
		next := &repository.PersonPhotoCursor{TakenAt: &t1, ID: 10}
		err := assertCursorAdvanced(prev, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
	t.Run("null zone smaller id advances (passes)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: nil, ID: 10}
		next := &repository.PersonPhotoCursor{TakenAt: nil, ID: 5}
		err := assertCursorAdvanced(prev, next)
		require.NoError(t, err)
	})
	t.Run("null zone larger id is regression (error)", func(t *testing.T) {
		prev := &repository.PersonPhotoCursor{TakenAt: nil, ID: 5}
		next := &repository.PersonPhotoCursor{TakenAt: nil, ID: 10}
		err := assertCursorAdvanced(prev, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAGINATION_STALLED")
	})
}
