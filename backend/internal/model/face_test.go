package model

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeEmbedding_JSONFormat 验证旧 JSON embedding 仍可正常解析。
func TestDecodeEmbedding_JSONFormat(t *testing.T) {
	payload := []byte(`[0.1,0.2,-0.3]`)
	got := DecodeEmbedding(payload)
	require.NotNil(t, got, "JSON embedding must decode")
	require.Len(t, got, 3)
	assert.InDeltaSlice(t, []float32{0.1, 0.2, -0.3}, got, 1e-5)
}

// TestDecodeEmbedding_JSONEmptyArray 验证空 JSON 数组解析为空切片（非 nil）。
func TestDecodeEmbedding_JSONEmptyArray(t *testing.T) {
	got := DecodeEmbedding([]byte(`[]`))
	require.NotNil(t, got, "empty JSON array must decode to non-nil empty slice")
	assert.Empty(t, got)
}

// TestDecodeEmbedding_BinaryRoundTrip 验证 EncodeEmbedding/DecodeEmbedding 往返一致。
func TestDecodeEmbedding_BinaryRoundTrip(t *testing.T) {
	input := []float32{0.1, -0.2, 0.3, -0.4, 0.5, 1.0, -1.0, 0.0}
	got := DecodeEmbedding(EncodeEmbedding(input))
	require.NotNil(t, got)
	require.Len(t, got, len(input))
	assert.InDeltaSlice(t, input, got, 1e-6)
}

// TestDecodeEmbedding_EmptyReturnsNil 验证空 payload 返回 nil。
func TestDecodeEmbedding_EmptyReturnsNil(t *testing.T) {
	assert.Nil(t, DecodeEmbedding(nil))
	assert.Nil(t, DecodeEmbedding([]byte{}))
}

// TestDecodeEmbedding_BinaryFirstByte0x5B 验证 raw binary embedding 首字节为 0x5B
// （等同 ASCII '['）时仍按 binary 正确解析，不会被误判为 JSON 而返回 nil。
// 这是线上 identity profile ANN rebuild 持续失败的回归覆盖。
func TestDecodeEmbedding_BinaryFirstByte0x5B(t *testing.T) {
	// 搜索一个首字节为 0x5B 的合法（非 NaN/Inf）float32。
	var value float32
	found := false
	for bits := uint32(0); bits < 0xFFFFFFFF; bits++ {
		if byte(bits) == '[' { // 0x5B
			v := math.Float32frombits(bits)
			if !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) {
				value = v
				found = true
				break
			}
		}
	}
	require.True(t, found, "must find a valid float32 whose little-endian first byte is 0x5B")

	// 构造一个首字节为 0x5B 的 binary embedding（2 个 float32）。
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint32(payload[0:4], math.Float32bits(value))
	binary.LittleEndian.PutUint32(payload[4:8], math.Float32bits(float32(0.42)))
	require.Equal(t, byte('['), payload[0], "first byte must be 0x5B to reproduce the bug")

	got := DecodeEmbedding(payload)
	require.NotNil(t, got, "binary embedding with 0x5B first byte must not be misjudged as JSON")
	require.Len(t, got, 2)
	assert.InDelta(t, float64(value), float64(got[0]), 1e-6)
	assert.InDelta(t, 0.42, float64(got[1]), 1e-6)
}

// TestDecodeEmbedding_BinaryFirstByte0x5B_RealLength 验证 2048 字节（512 float32，
// 与线上 NAS center 309 相同长度）且首字节为 0x5B 的 binary embedding 可解析。
func TestDecodeEmbedding_BinaryFirstByte0x5B_RealLength(t *testing.T) {
	// 搜索一个首字节为 0x5B 的合法 float32。
	var firstBits uint32
	found := false
	for bits := uint32(0); bits < 0xFFFFFFFF; bits++ {
		if byte(bits) == '[' {
			v := math.Float32frombits(bits)
			if !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) && v != 0 {
				firstBits = bits
				found = true
				break
			}
		}
	}
	require.True(t, found)

	const dim = 512
	payload := make([]byte, dim*4)
	binary.LittleEndian.PutUint32(payload[0:4], firstBits)
	for i := 1; i < dim; i++ {
		binary.LittleEndian.PutUint32(payload[i*4:], math.Float32bits(float32(i)*0.001))
	}
	require.Equal(t, byte('['), payload[0])
	require.Equal(t, 2048, len(payload), "must match NAS center 309 payload length")

	got := DecodeEmbedding(payload)
	require.NotNil(t, got)
	require.Len(t, got, dim)
	assert.Equal(t, math.Float32frombits(firstBits), got[0])
}

// TestDecodeEmbedding_NonJSONNonMultipleOf4ReturnsNil 验证非 JSON 且长度不是 4 的
// 倍数时返回 nil。
func TestDecodeEmbedding_NonJSONNonMultipleOf4ReturnsNil(t *testing.T) {
	payload := []byte{1, 2, 3}
	assert.Nil(t, DecodeEmbedding(payload))
}

// TestDecodeEmbedding_JSONPrefixInvalidNonMultipleOf4ReturnsNil 验证以 '[' 开头、
// JSON 无效、且长度不是 4 的倍数时返回 nil。
func TestDecodeEmbedding_JSONPrefixInvalidNonMultipleOf4ReturnsNil(t *testing.T) {
	// 5 字节：JSON 无效且长度不是 4 的倍数，无法 fallback binary → 返回 nil。
	payload := []byte("[bad!")
	assert.Nil(t, DecodeEmbedding(payload))
}

// TestDecodeEmbedding_JSONPrefixInvalidButMultipleOf4FallsBackToBinary 验证以 '['
// 开头但 JSON 无效、长度是 4 的倍数时，fallback 到 binary 解析成功。
func TestDecodeEmbedding_JSONPrefixInvalidButMultipleOf4FallsBackToBinary(t *testing.T) {
	// 首字节为 0x5B，但整体不是合法 JSON（第二个 float32 的字节不是合法 JSON token）。
	// 构造 4 字节 binary（1 个 float32），首字节 0x5B，后 3 字节随意但不是 ']' 之类。
	payload := make([]byte, 4)
	payload[0] = '[' // 0x5B
	payload[1] = 0x00
	payload[2] = 0x00
	payload[3] = 0x00
	// 不是合法 JSON（"[bad" 类型），但长度 %4==0 → fallback binary。
	got := DecodeEmbedding(payload)
	require.NotNil(t, got, "must fall back to binary when JSON parse fails and length is multiple of 4")
	require.Len(t, got, 1)
	assert.Equal(t, math.Float32frombits(binary.LittleEndian.Uint32(payload)), got[0])
}
