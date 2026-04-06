package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIClient_GetTasks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/people/worker/tasks", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("X-API-Key"))
		assert.Equal(t, "test-worker", r.Header.Get("X-Worker-ID"))

		resp := model.Response{
			Success: true,
			Data: model.PeopleWorkerTasksResponse{
				Tasks: []model.PeopleWorkerTask{
					{ID: 1, PhotoID: 101, FilePath: "/photos/test1.jpg"},
					{ID: 2, PhotoID: 102, FilePath: "/photos/test2.jpg"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key", WithWorkerID("test-worker"))
	tasks, err := client.GetTasks(context.Background(), 10)

	require.NoError(t, err)
	assert.Len(t, tasks.Tasks, 2)
	assert.Equal(t, uint(1), tasks.Tasks[0].ID)
	assert.Equal(t, uint(101), tasks.Tasks[0].PhotoID)
}

func TestAPIClient_HeartbeatTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/people/worker/tasks/1/heartbeat", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var req model.PeopleWorkerHeartbeatRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, 50, req.Progress)
		assert.Equal(t, "processing", req.StatusMessage)

		resp := model.Response{
			Success: true,
			Data: model.PeopleWorkerHeartbeatResponse{
				LockExpiresAt: time.Now().Add(5 * time.Minute),
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key")
	heartbeat, err := client.HeartbeatTask(context.Background(), 1, 50, "processing")

	require.NoError(t, err)
	assert.True(t, heartbeat.LockExpiresAt.After(time.Now()))
}

func TestAPIClient_ReleaseTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/people/worker/tasks/1/release", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var req model.PeopleWorkerReleaseTaskRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "test error", req.Reason)
		assert.True(t, req.RetryLater)

		resp := model.Response{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key")
	err := client.ReleaseTask(context.Background(), 1, "test error", true)

	require.NoError(t, err)
}

func TestAPIClient_SubmitResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/people/worker/results", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var req model.PeopleWorkerSubmitResultsRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Len(t, req.Results, 1)
		assert.Equal(t, uint(101), req.Results[0].PhotoID)

		resp := model.Response{
			Success: true,
			Data: model.PeopleWorkerSubmitResultsResponse{
				Processed: 1,
				Errors:    []string{},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key")
	results := []model.PeopleDetectionResult{
		{
			PhotoID: 101,
			TaskID:  1,
			Faces: []model.PeopleDetectionFace{
				{
					BBox: model.BoundingBox{
						X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2,
					},
					Confidence:   0.95,
					QualityScore: 0.9,
					Embedding:    []float32{0.1, 0.2, 0.3},
				},
			},
			ProcessingTimeMS: 100,
		},
	}

	resp, err := client.SubmitResults(context.Background(), results)

	require.NoError(t, err)
	assert.Equal(t, 1, resp.Processed)
}

func TestAPIClient_AcquireRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/people/runtime/acquire", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		resp := model.Response{
			Success: true,
			Data: model.PeopleWorkerRuntimeLeaseResponse{
				LeaseExpiresAt: time.Now().Add(30 * time.Second),
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key", WithWorkerID("test-worker"))
	lease, err := client.AcquireRuntime(context.Background())

	require.NoError(t, err)
	assert.True(t, lease.LeaseExpiresAt.After(time.Now()))
}

func TestAPIClient_ReleaseRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/people/runtime/release", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		resp := model.Response{Success: true, Message: "Runtime released"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key", WithWorkerID("test-worker"))
	err := client.ReleaseRuntime(context.Background())

	require.NoError(t, err)
}

func TestAPIClient_RetryOnServerError(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		resp := model.Response{
			Success: true,
			Data: model.PeopleWorkerTasksResponse{
				Tasks: []model.PeopleWorkerTask{},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-api-key",
		WithRetry(3, 10*time.Millisecond),
	)
	_, err := client.GetTasks(context.Background(), 10)

	require.NoError(t, err)
	assert.Equal(t, 3, attemptCount)
}
