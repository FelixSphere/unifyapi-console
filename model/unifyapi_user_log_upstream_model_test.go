package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: a consume log records the model a channel mapped the request
// to ("z-ai/glm-5.3" for a request that asked for glm-5.3). That names the
// upstream, so the user who made the request is not shown it. An admin still
// is: it is how a mapping is checked.
func TestAUserIsNotShownTheUpstreamModelTheirRequestWasMappedTo(t *testing.T) {
	const userID = 26112601
	row := &Log{
		UserId:    userID,
		Type:      LogTypeConsume,
		CreatedAt: common.GetTimestamp(),
		ModelName: "glm-5.3",
		Other: common.MapToJsonStr(map[string]interface{}{
			"model_ratio":         0.3,
			"frt":                 412.0,
			"is_model_mapped":     true,
			"upstream_model_name": "z-ai/glm-5.3",
		}),
	}
	require.NoError(t, LOG_DB.Create(row).Error)
	t.Cleanup(func() { LOG_DB.Delete(&Log{}, row.Id) })

	userLogs, _, err := GetUserLogs(userID, LogTypeConsume, 0, 0, "", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Len(t, userLogs, 1)
	assert.Equal(t, "glm-5.3", userLogs[0].ModelName)
	assert.NotContains(t, userLogs[0].Other, "z-ai")
	shownToUser, err := common.StrToMap(userLogs[0].Other)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"model_ratio": 0.3, "frt": 412.0}, shownToUser)

	adminLogs, _, err := GetAllLogs(LogTypeConsume, 0, 0, "glm-5.3", "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Len(t, adminLogs, 1)
	shownToAdmin, err := common.StrToMap(adminLogs[0].Other)
	require.NoError(t, err)
	assert.Equal(t, true, shownToAdmin["is_model_mapped"])
	assert.Equal(t, "z-ai/glm-5.3", shownToAdmin["upstream_model_name"])
}
