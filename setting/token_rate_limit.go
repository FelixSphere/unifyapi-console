// UNIFYAPI-BRAND: ours. Per-customer token rate limiting (TPM).
//
// The request-count limiter in rate_limit.go is the wrong shape for LLM load:
// one request carrying a 200k-token context and one carrying "hello" cost the
// same there, while costing wildly different amounts of an upstream's capacity
// and of our money. This adds the dimension that actually tracks spend.
//
// It deliberately reuses ModelRequestRateLimitDurationMinutes, so an operator
// reasons about one window for both counters rather than two.
package setting

import (
	"encoding/json"
	"sync"
)

var (
	// 0 means unlimited, matching the request-count limiter's convention.
	ModelRequestTokenLimitCount = 0

	// Per-group overrides, group name -> tokens per window. A group that is not
	// listed falls back to the global value above, so onboarding a customer
	// never throttles them by omission.
	ModelRequestTokenLimitGroup = map[string]int{}

	modelRequestTokenLimitMutex sync.RWMutex
)

func ModelRequestTokenLimitGroup2JSONString() string {
	modelRequestTokenLimitMutex.RLock()
	defer modelRequestTokenLimitMutex.RUnlock()

	out, err := json.Marshal(ModelRequestTokenLimitGroup)
	if err != nil {
		return "{}"
	}
	return string(out)
}

func UpdateModelRequestTokenLimitGroupByJSONString(jsonStr string) error {
	modelRequestTokenLimitMutex.Lock()
	defer modelRequestTokenLimitMutex.Unlock()

	parsed := map[string]int{}
	if jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			return err
		}
	}
	ModelRequestTokenLimitGroup = parsed
	return nil
}

// GetGroupTokenLimit returns the token allowance for a group, and whether the
// group had an explicit override.
func GetGroupTokenLimit(group string) (limit int, found bool) {
	modelRequestTokenLimitMutex.RLock()
	defer modelRequestTokenLimitMutex.RUnlock()

	limit, found = ModelRequestTokenLimitGroup[group]
	return limit, found
}
