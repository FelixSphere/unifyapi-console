package service

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A channel that answers 200 and then says nothing is only visible at
// settlement: there is no error to classify, just nothing to bill. The affinity
// failover reads that through ChannelAffinityRequestHadNoOutput, so settlement
// has to raise it, and must not raise it for an answer that was billed.
func TestSettlementFlagsAResponseWithNothingToBill(t *testing.T) {
	f := setupSettlementDB(t)

	settle := func(name string, usage *dto.Usage) *gin.Context {
		requestId := fmt.Sprintf("req-no-output-%s", name)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		ctx.Set(common.RequestIdKey, requestId)
		ctx.Set("username", "settle")

		relayInfo := &relaycommon.RelayInfo{
			UserId:                  f.userId,
			TokenId:                 f.tokenId,
			TokenKey:                f.tokenKey,
			RequestId:               requestId,
			RelayFormat:             types.RelayFormatOpenAI,
			FinalRequestRelayFormat: types.RelayFormatOpenAI,
			OriginModelName:         "loadtest-mock",
			StartTime:               time.Now(),
			ChannelMeta:             &relaycommon.ChannelMeta{ChannelId: 199},
			PriceData: hosttypes.PriceData{
				ModelRatio:      1,
				CompletionRatio: 1,
				GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
			},
		}
		require.Nil(t, PreConsumeBilling(ctx, 0, relayInfo))
		require.False(t, ChannelAffinityRequestHadNoOutput(ctx), "nothing is known before settlement")
		PostTextConsumeQuota(ctx, relayInfo, usage, nil)
		return ctx
	}

	require.True(t, ChannelAffinityRequestHadNoOutput(settle("empty-usage", &dto.Usage{})))
	require.True(t, ChannelAffinityRequestHadNoOutput(settle("no-usage", nil)))
	require.False(t, ChannelAffinityRequestHadNoOutput(settle("billed", &dto.Usage{
		PromptTokens:     settlementPrompt,
		CompletionTokens: settlementCompletion,
		TotalTokens:      settlementPrompt + settlementCompletion,
	})))
}
