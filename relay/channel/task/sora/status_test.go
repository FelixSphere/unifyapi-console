package sora

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnknownVideoStatusRemainsRetryable(t *testing.T) {
	a := &TaskAdaptor{}
	result, err := a.ParseTaskResult([]byte(`{"id":"upstream_job","task_id":"upstream_job","model":"seedance-2.5","status":"unknown","progress":0,"completed_at":0}`))
	require.ErrorContains(t, err, "retry polling")
	require.Nil(t, result)
	// The same upstream job becomes readable without a second submission.
	result, err = a.ParseTaskResult([]byte(`{"id":"upstream_job","status":"in_progress","progress":50}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusInProgress, result.Status)
	require.Equal(t, "50%", result.Progress)
	result, err = a.ParseTaskResult([]byte(`{"id":"upstream_job","status":"completed"}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
}

func TestExplicitVideoFailureStillTerminates(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"id":"upstream_job","status":"failed","error":{"code":"generation_failed","message":"generation rejected"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, result.Status)
	require.Equal(t, "generation rejected", result.Reason)
}

func TestUnknownVideoStatusWithExplicitErrorStillFails(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"id":"upstream_job","status":"unknown","error":{"message":"permission denied"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, result.Status)
	require.Equal(t, "permission denied", result.Reason)
}
