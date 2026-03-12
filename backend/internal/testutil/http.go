package testutil

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// PerformJSONRequest creates an httptest recorder, builds a gin test context,
// and calls the handler function directly. Returns the recorder for assertion.
func PerformJSONRequest(t *testing.T, method, path string, body []byte, params gin.Params, fn func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = params
	ctx.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	fn(ctx)
	return recorder
}

// DecodeAPIResponse unmarshals the response body into model.Response.
func DecodeAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) model.Response {
	t.Helper()

	var resp model.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("testutil: decode response: %v\nbody: %s", err, recorder.Body.String())
	}
	return resp
}

// DecodeResponseData extracts the Data field from a model.Response and unmarshals
// it into the target type T via JSON round-trip.
func DecodeResponseData[T any](t *testing.T, response model.Response) T {
	t.Helper()

	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("testutil: marshal response data: %v", err)
	}

	var data T
	if err := json.Unmarshal(dataJSON, &data); err != nil {
		t.Fatalf("testutil: unmarshal response data into %T: %v", data, err)
	}
	return data
}
