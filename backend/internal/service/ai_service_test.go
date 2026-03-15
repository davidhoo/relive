package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIService_GetProvider_Nil(t *testing.T) {
	svc := &aiService{provider: nil}

	_, err := svc.GetProvider()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestAIService_GetTaskStatus_Nil(t *testing.T) {
	svc := &aiService{}

	status := svc.GetTaskStatus()
	assert.Nil(t, status)
}

func TestAIService_GetTaskStatus_WithTask(t *testing.T) {
	svc := &aiService{
		currentTask: &AnalyzeTask{ID: "task-1", Status: AnalyzeTaskStatusRunning, TotalCount: 10},
	}

	status := svc.GetTaskStatus()
	require.NotNil(t, status)
	assert.Equal(t, "task-1", status.ID)
	assert.Equal(t, AnalyzeTaskStatusRunning, status.Status)
}

func TestAIService_GetBackgroundLogs_Empty(t *testing.T) {
	svc := &aiService{}

	logs := svc.GetBackgroundLogs()
	assert.Empty(t, logs)
}

func TestAIService_GetBackgroundLogs_WithLogs(t *testing.T) {
	svc := &aiService{
		backgroundLogs: []string{"log1", "log2"},
	}

	logs := svc.GetBackgroundLogs()
	assert.Len(t, logs, 2)
	assert.Equal(t, "log1", logs[0])
}

func TestAIService_AnalyzeBatch_NilProvider(t *testing.T) {
	svc := &aiService{provider: nil}

	_, err := svc.AnalyzeBatch(10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestAnalyzeTask_IsRunning(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{AnalyzeTaskStatusRunning, true},
		{AnalyzeTaskStatusSleeping, true},
		{AnalyzeTaskStatusStopping, true},
		{AnalyzeTaskStatusCompleted, false},
		{AnalyzeTaskStatusFailed, false},
		{AnalyzeTaskStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			task := &AnalyzeTask{Status: tt.status}
			assert.Equal(t, tt.expected, task.IsRunning())
		})
	}
}
