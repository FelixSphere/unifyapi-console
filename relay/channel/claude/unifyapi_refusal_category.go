package claude

import (
	"strings"

	"github.com/tidwall/gjson"
)

// UNIFYAPI-FORK: fork-owned. Records WHICH safety classifier declined a
// request, next to the fact that one did.
//
// Anthropic bills a refusal that arrives before any output only for some
// categories (bio, frontier_llm, reasoning_extraction as of 2026-09) and not
// for the others (cyber, general_harms, or a null category), while the token
// counts appear in `usage` either way. We bill from `usage`, so without the
// category a refused row cannot be told apart from one the vendor did not
// charge for. This only records it; it changes no response and no charge.

const claudeRefusalRejectReason = "claude_stop_reason=refusal"

// stop_details sits beside stop_reason: at the top level of a non-streaming
// message, inside `delta` on a streamed message_delta.
var claudeStopDetailsPaths = []string{"stop_details", "delta.stop_details"}

// claudeRefusalRejectReasonFor reads the category out of the raw upstream
// JSON rather than the DTO, so the DTO that is re-marshalled to the caller
// stays exactly as it was. "category=none" is the vendor's explicit null (a
// permanent value, not a placeholder); no category suffix at all means the
// upstream sent no stop_details, e.g. a reseller that dropped the field.
func claudeRefusalRejectReasonFor(raw string) string {
	for _, path := range claudeStopDetailsPaths {
		details := gjson.Get(raw, path)
		if !details.IsObject() {
			continue
		}
		category := strings.TrimSpace(details.Get("category").String())
		if category == "" {
			category = "none"
		}
		return claudeRefusalRejectReason + ", category=" + category
	}
	return claudeRefusalRejectReason
}
