package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelOpsShowsSub2APIAccountsToRootOnly(t *testing.T) {
	t.Setenv("UNIFYAPI_SUB2API_ADMIN_URL", "https://sub2api.example")
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("role", role)
		GetChannelOps(c)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"retry_times":`)
		if role == common.RoleRootUser {
			require.Contains(t, w.Body.String(), `"sub2api_accounts_url":"https://sub2api.example/admin/accounts"`)
		} else {
			require.NotContains(t, w.Body.String(), "sub2api_accounts_url")
		}
	}
}
