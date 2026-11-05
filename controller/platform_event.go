package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: the platform log, read side. See model/platform_event.go for
// what it holds and why.
//
// Root only, and deliberately not merely admin. These rows carry other
// customers' source addresses in full, which the usage log does not: a usage
// log row belongs to the user looking at it, a platform log row belongs to
// whoever was turned away, and that is a different thing to hand out. The
// route is registered under a RootAuth group so the check cannot be forgotten
// here.
func GetPlatformEvents(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	status, _ := strconv.Atoi(c.Query("status"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	events, total, err := model.SearchPlatformEvents(model.PlatformEventQuery{
		Source:    c.Query("source"),
		Kind:      c.Query("kind"),
		ClientIP:  c.Query("client_ip"),
		KeyPrefix: c.Query("key_prefix"),
		Status:    status,
		StartAt:   startTimestamp,
		EndAt:     endTimestamp,
		Page:      pageInfo.GetPage(),
		PageSize:  pageInfo.GetPageSize(),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(events)
	common.ApiSuccess(c, pageInfo)
}
