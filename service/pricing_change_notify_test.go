package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type sentNotice struct {
	userId int
	email  string
	notify dto.Notify
}

// setupPricingNoticeDB gives the run a real (sqlite) options table, user table,
// consume log and ability table, and captures every notification instead of
// sending it.
func setupPricingNoticeDB(t *testing.T) *[]sentNotice {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Log{}, &model.Ability{}, &model.PricingConfigHistory{}))

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	delete(common.OptionMap, model.PricingBaselineAnnouncedKey)
	delete(common.OptionMap, model.PricingChangeNotifyEnabledKey)
	common.OptionMapRWMutex.Unlock()

	var sent []sentNotice
	previousSend := sendPricingChangeNotify
	sendPricingChangeNotify = func(userId int, email string, _ dto.UserSetting, data dto.Notify) error {
		sent = append(sent, sentNotice{userId: userId, email: email, notify: data})
		return nil
	}

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		sendPricingChangeNotify = previousSend
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, model.PricingBaselineAnnouncedKey)
		delete(common.OptionMap, model.PricingChangeNotifyEnabledKey)
		common.OptionMapRWMutex.Unlock()
	})
	return &sent
}

func serveModels(t *testing.T, models ...string) {
	t.Helper()
	for _, name := range models {
		require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: name, ChannelId: 1, Enabled: true}).Error)
	}
}

func consumed(t *testing.T, userId int, modelName string, at time.Time) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: userId, Type: model.LogTypeConsume, ModelName: modelName, CreatedAt: at.Unix(), Quota: 10,
	}).Error)
}

func storeBaseline(t *testing.T, baseline map[string]AnnouncedPrice) {
	t.Helper()
	raw, err := common.Marshal(baseline)
	require.NoError(t, err)
	require.NoError(t, model.SaveAnnouncedPricingBaseline(string(raw)))
}

func storedBaseline(t *testing.T) map[string]AnnouncedPrice {
	t.Helper()
	out := map[string]AnnouncedPrice{}
	require.NoError(t, common.Unmarshal([]byte(model.GetAnnouncedPricingBaseline()), &out))
	return out
}

func TestTheFirstRunRecordsTheBaselineAndTellsNobody(t *testing.T) {
	sent := setupPricingNoticeDB(t)
	serveModels(t, "gpt-4o")
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "root", AffCode: "aff-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Email: "root@example.com"}).Error)
	consumed(t, 1, "gpt-4o", time.Now())

	result, err := RunPricingChangeNotice(time.Now())
	require.NoError(t, err)
	assert.True(t, result.Seeded)
	assert.Empty(t, *sent, "there is no 'before' on the first run, so nothing is a change")
	assert.Equal(t, CurrentPricingBaseline(), storedBaseline(t))
}

func TestARepricedModelIsAnnouncedToItsRecentUsersAndAdmins(t *testing.T) {
	sent := setupPricingNoticeDB(t)
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	serveModels(t, "gpt-4o", "claude-opus-5", "legacy-model")

	gpt4o, ok := ratio_setting.CatalogEntryFor("gpt-4o")
	require.True(t, ok)
	previous := CurrentPricingBaseline()
	was := previous["gpt-4o"]
	was.InputUSD = 2.0 // what customers were last told
	previous["gpt-4o"] = was
	previous["legacy-model"] = AnnouncedPrice{InputUSD: 1, OutputUSD: 2} // since dropped from the catalog
	storeBaseline(t, previous)

	for _, user := range []model.User{
		{Id: 1, Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Email: "root@example.com"},
		{Id: 2, Username: "acme", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "acme@example.com"},
		{Id: 3, Username: "dormant", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "dormant@example.com"},
		{Id: 4, Username: "opus-only", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "opus@example.com"},
		{Id: 5, Username: "banned", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, Email: "banned@example.com"},
		{Id: 6, Username: "pager", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "pager@example.com",
			Setting: `{"notify_type":"bark","bark_url":"https://bark.example/key"}`},
	} {
		user.AffCode = "aff-" + user.Username // unique index; irrelevant here
		require.NoError(t, model.DB.Create(&user).Error)
	}
	consumed(t, 2, "gpt-4o", now.Add(-2*24*time.Hour))
	consumed(t, 2, "gpt-4o", now.Add(-1*time.Hour))
	consumed(t, 3, "gpt-4o", now.Add(-40*24*time.Hour))
	consumed(t, 4, "claude-opus-5", now.Add(-time.Hour))
	consumed(t, 5, "gpt-4o", now.Add(-time.Hour))
	consumed(t, 6, "legacy-model", now.Add(-time.Hour))

	result, err := RunPricingChangeNotice(now)
	require.NoError(t, err)
	assert.False(t, result.Seeded)
	assert.Equal(t, 2, result.Changes)
	assert.Equal(t, []string{"gpt-4o", "legacy-model"}, result.Models)
	assert.Equal(t, 3, result.Notified)
	assert.Zero(t, result.Failed)

	byUser := map[int]sentNotice{}
	for _, notice := range *sent {
		byUser[notice.userId] = notice
	}
	require.Len(t, byUser, 3, "root (admin), acme (used gpt-4o), pager (used legacy-model)")
	assert.NotContains(t, byUser, 3, "a customer who last called the model 40 days ago is not told")
	assert.NotContains(t, byUser, 4, "a customer of an unchanged model is not told")
	assert.NotContains(t, byUser, 5, "a disabled account is not emailed")

	acme := byUser[2].notify
	assert.Equal(t, dto.NotifyTypePricingChange, acme.Type)
	assert.Contains(t, acme.Title, "gpt-4o")
	assert.Contains(t, acme.Content, "$2 in")
	assert.Contains(t, acme.Content, "$"+trimFloat(gpt4o.InputUSD)+" in")
	assert.Contains(t, acme.Content, "22 September 2026")
	assert.Contains(t, acme.Content, "/pricing")
	assert.NotContains(t, acme.Content, "legacy-model", "a customer hears only about the models they use")

	root := byUser[1].notify
	assert.Contains(t, root.Title, "2 models")
	assert.Contains(t, root.Content, "gpt-4o")
	assert.Contains(t, root.Content, "legacy-model")
	assert.Contains(t, root.Content, "withdrawn")

	pager := byUser[6].notify
	assert.NotContains(t, pager.Content, "<table", "push channels get plain text")
	assert.Contains(t, pager.Content, "legacy-model: withdrawn")

	assert.Equal(t, CurrentPricingBaseline(), storedBaseline(t), "the announced baseline advances once the notices went out")

	*sent = nil
	again, err := RunPricingChangeNotice(now.Add(time.Hour))
	require.NoError(t, err)
	assert.Zero(t, again.Changes)
	assert.Empty(t, *sent, "a change is announced once")
}

func trimFloat(v float64) string { return strings.TrimPrefix(usd(v), "$") }

func TestAChangeOnAModelNobodyIsRoutedToAdvancesQuietly(t *testing.T) {
	sent := setupPricingNoticeDB(t)
	serveModels(t, "gpt-4o")
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "root", AffCode: "aff-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Email: "root@example.com"}).Error)

	previous := CurrentPricingBaseline()
	was := previous["glm-4.7"]
	was.OutputUSD = 99
	previous["glm-4.7"] = was
	storeBaseline(t, previous)

	result, err := RunPricingChangeNotice(time.Now())
	require.NoError(t, err)
	assert.Zero(t, result.Changes)
	assert.True(t, result.Announced)
	assert.Empty(t, *sent)
	assert.Equal(t, CurrentPricingBaseline(), storedBaseline(t),
		"the stored baseline must catch up, or listing glm-4.7 later would announce a change from a price nobody paid")
}

func TestDiffPricingBaselineClassifiesEachKind(t *testing.T) {
	previous := map[string]AnnouncedPrice{
		"same":       {InputUSD: 1, OutputUSD: 2},
		"moved":      {InputUSD: 1, OutputUSD: 2},
		"cache-only": {InputUSD: 1, OutputUSD: 2, CacheReadUSD: 0.1},
		"gone-sold":  {InputUSD: 1, OutputUSD: 2},
		"gone-idle":  {InputUSD: 1, OutputUSD: 2},
		"unsold":     {InputUSD: 1, OutputUSD: 2},
	}
	current := map[string]AnnouncedPrice{
		"same":       {InputUSD: 1, OutputUSD: 2},
		"moved":      {InputUSD: 1.5, OutputUSD: 2},
		"cache-only": {InputUSD: 1, OutputUSD: 2, CacheReadUSD: 0.2},
		"new":        {PerCallUSD: 0.1, PriceUnit: "second"},
		"unsold":     {InputUSD: 9, OutputUSD: 9},
	}
	served := map[string]bool{"same": true, "moved": true, "cache-only": true, "gone-sold": true, "new": true}

	changes := DiffPricingBaseline(previous, current, served)
	kinds := map[string]PriceChangeKind{}
	for _, change := range changes {
		kinds[change.Model] = change.Kind
	}
	assert.Equal(t, map[string]PriceChangeKind{
		"moved":      PriceChangeChanged,
		"cache-only": PriceChangeChanged,
		"new":        PriceChangeAdded,
		"gone-sold":  PriceChangeRemoved,
	}, kinds)
	assert.NotContains(t, kinds, "unsold", "a change on a model no channel serves is not a customer-facing event")
	assert.NotContains(t, kinds, "gone-idle", "a withdrawn row nobody could call needs no notice")
	assert.Equal(t, "cache-only", changes[0].Model, "sorted by model name")
}

func TestPriceLinesReadLikeThePricingPage(t *testing.T) {
	assert.Equal(t, "$2.5 in / $10 out / $1.25 cached read per 1M tokens",
		priceLine(AnnouncedPrice{InputUSD: 2.5, OutputUSD: 10, CacheReadUSD: 1.25}))
	assert.Equal(t, "$5 in / $25 out / $0.5 cached read / $6.25 cache write per 1M tokens",
		priceLine(AnnouncedPrice{InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25}))
	assert.Equal(t, "$0.075 in / $0.3 out per 1M tokens",
		priceLine(AnnouncedPrice{InputUSD: 0.075, OutputUSD: 0.3}), "no rounding of a 7.5 cent rate")
	assert.Equal(t, "$0.14 per second of output", priceLine(AnnouncedPrice{PerCallUSD: 0.14, PriceUnit: "second"}))
	assert.Equal(t, "$0.04 per request", priceLine(AnnouncedPrice{PerCallUSD: 0.04}))
}

func TestTheNoticeCanBeSwitchedOffButIsOnByDefault(t *testing.T) {
	setupPricingNoticeDB(t)
	assert.True(t, model.PricingChangeNotifyEnabled())
	require.NoError(t, model.UpdateOption(model.PricingChangeNotifyEnabledKey, "false"))
	assert.False(t, model.PricingChangeNotifyEnabled())
	require.NoError(t, model.UpdateOption(model.PricingChangeNotifyEnabledKey, "true"))
	assert.True(t, model.PricingChangeNotifyEnabled())
}
