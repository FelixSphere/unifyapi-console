package controller

// UNIFYAPI-FORK: FelixSphere's single Stripe account is shared by the console,
// Unify Compute and Unify Data, and every webhook endpoint on it receives the
// whole account's checkout events. The console tags what it creates with
// metadata.product=unify-api and answers everything else 200 without touching
// an order, so another product's sale is never reported here as a failed
// top-up. Unify Payment ADR-0001, phase P0.

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/stripe/stripe-go/v81"
)

const (
	stripeProductMetadataKey = "product"
	stripeProductUnifyAPI    = "unify-api"
)

func stripeProductMetadata() map[string]string {
	return map[string]string{stripeProductMetadataKey: stripeProductUnifyAPI}
}

// foreignStripeEventReason names why an event belongs to another product on
// the shared account, or returns "" when the console must handle it.
//
// A session without metadata.product is still ours if its client_reference_id
// is a console order: sessions created before the tag existed carry none. A
// lookup that fails, as opposed to finding nothing, is never read as foreign —
// that would acknowledge a real payment and drop its credit.
func foreignStripeEventReason(ctx context.Context, event stripe.Event) string {
	// Read metadata by hand: Event.GetObjectValue panics on a null or missing
	// metadata object instead of returning "".
	if metadata, ok := event.Data.Object["metadata"].(map[string]any); ok {
		if product, _ := metadata[stripeProductMetadataKey].(string); product != "" && product != stripeProductUnifyAPI {
			return "product=" + product
		}
	}

	if !strings.HasPrefix(string(event.Type), "checkout.session.") {
		return ""
	}
	referenceId, _ := event.Data.Object["client_reference_id"].(string)
	if referenceId == "" {
		return "no_client_reference_id"
	}
	exists, err := model.StripeOrderExists(referenceId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 订单归属查询失败，按本产品事件处理 trade_no=%s error=%q", referenceId, err.Error()))
		return ""
	}
	if !exists {
		return "unknown_client_reference_id"
	}
	return ""
}
