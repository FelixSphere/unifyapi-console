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
	// Attach who each address has been seen used by. Returned as a separate
	// map rather than folded into each row, because it is a property of the
	// ADDRESS and not of the event -- a shared exit maps several parties onto
	// one line, and a row that simply said "user: Chris" would read as proof
	// when it is only a correlation.
	ips := make([]string, 0, len(events))
	for _, e := range events {
		if e.ClientIP != "" {
			ips = append(ips, e.ClientIP)
		}
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(events)
	common.ApiSuccess(c, gin.H{
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
		"total":     total,
		"items":     events,
		"ip_owners": model.ResolveIPOwners(ips),
	})
}
