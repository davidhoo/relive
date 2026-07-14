package handler

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

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
