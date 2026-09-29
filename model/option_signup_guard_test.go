package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsInvalidRegisterIPDailyLimit(t *testing.T) {
	for _, value := range []string{"", "-1", "1.5", "three"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("RegisterIPDailyLimit", value))
		})
	}
	require.NoError(t, validateOptionValue("RegisterIPDailyLimit", "0"))
	require.NoError(t, validateOptionValue("RegisterIPDailyLimit", "5"))
}

func TestSignupGuardOptionsApplyToRuntimeSettings(t *testing.T) {
	previousEnabled, previousList, previousLimit := common.EmailDomainBlocklistEnabled, common.EmailDomainBlocklist, common.RegisterIPDailyLimit
	keys := []string{"EmailDomainBlocklistEnabled", "EmailDomainBlocklist", "RegisterIPDailyLimit"}
	common.OptionMapRWMutex.Lock()
	// Other tests in this package branch on whether OptionMap exists at all,
	// so a map created here must not outlive the test.
	createdOptionMap := common.OptionMap == nil
	if createdOptionMap {
		common.OptionMap = map[string]string{}
	}
	previousOptions := map[string]string{}
	for _, key := range keys {
		if value, ok := common.OptionMap[key]; ok {
			previousOptions[key] = value
		}
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.EmailDomainBlocklistEnabled, common.EmailDomainBlocklist, common.RegisterIPDailyLimit = previousEnabled, previousList, previousLimit
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if createdOptionMap {
			common.OptionMap = nil
			return
		}
		for _, key := range keys {
			if value, ok := previousOptions[key]; ok {
				common.OptionMap[key] = value
			} else {
				delete(common.OptionMap, key)
			}
		}
	})

	require.NoError(t, updateOptionMap("EmailDomainBlocklistEnabled", "false"))
	require.NoError(t, updateOptionMap("EmailDomainBlocklist", "Example-Temp.com\n\n*.burner.test\n"))
	require.NoError(t, updateOptionMap("RegisterIPDailyLimit", "7"))

	assert.False(t, common.EmailDomainBlocklistEnabled)
	assert.Equal(t, []string{"example-temp.com", "burner.test"}, common.EmailDomainBlocklist)
	assert.Equal(t, 7, common.RegisterIPDailyLimit)

	// A malformed limit already stored in the database keeps the previous
	// limit instead of silently turning the cap off.
	require.NoError(t, updateOptionMap("RegisterIPDailyLimit", "-4"))
	assert.Equal(t, 7, common.RegisterIPDailyLimit)
}
