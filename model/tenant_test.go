/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTenantTestDB swaps the package-level DB for an in-memory SQLite one, the
// same approach the existing model tests use.
func setupTenantTestDB(t *testing.T) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Tenant{}, &User{}, &Log{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
}

func createTestUser(t *testing.T, username string, quota int) *User {
	t.Helper()
	// AffCode carries a unique index, so it must be distinct per user -- the
	// real insert path assigns a random one. Derive it from the username so
	// failures stay readable.
	user := &User{
		Username:    username,
		DisplayName: username,
		Quota:       quota,
		AffCode:     "aff-" + username,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func TestSlugFromName(t *testing.T) {
	cases := map[string]string{
		"Acme Inc.":      "acme-inc",
		"ACME":           "acme",
		"a  b":           "a-b",
		"--weird--":      "weird",
		"user_name99":    "user-name99",
		"北京公司":           "tenant", // reduces to nothing, must not be empty
		"":               "tenant",
		"Trailing dash-": "trailing-dash",
	}
	for in, want := range cases {
		assert.Equal(t, want, slugFromName(in), "slug for %q", in)
	}
}

func TestSlugFromNameTruncatesWithoutTrailingDash(t *testing.T) {
	long := ""
	for i := 0; i < 30; i++ {
		long += "ab "
	}
	got := slugFromName(long)
	assert.LessOrEqual(t, len(got), 48)
	assert.NotEmpty(t, got)
	assert.NotEqual(t, byte('-'), got[len(got)-1], "must not end in a dash")
}

func TestEnsureTenantForUserMovesBalanceOntoTenant(t *testing.T) {
	setupTenantTestDB(t)
	user := createTestUser(t, "alice", 5000)

	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)
	require.NotNil(t, tenant)

	assert.Equal(t, user.Id, tenant.OwnerId)
	assert.Equal(t, "alice", tenant.Slug)
	assert.Equal(t, TenantStatusEnabled, tenant.Status)

	// The balance must live in exactly one place, or the two diverge.
	assert.Equal(t, 5000, tenant.Quota, "tenant takes over the starting balance")

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, tenant.Id, reloaded.TenantId)
	assert.Equal(t, 0, reloaded.Quota, "user row must not keep a second balance")
}

func TestEnsureTenantForUserIsIdempotent(t *testing.T) {
	setupTenantTestDB(t)
	user := createTestUser(t, "bob", 100)

	first, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)
	second, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	assert.Equal(t, first.Id, second.Id)

	total, err := CountTenants()
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "must not create a duplicate tenant")

	// And the balance must not be double-counted on the second call.
	assert.Equal(t, 100, second.Quota)
}

func TestEnsureTenantForUserResolvesSlugCollision(t *testing.T) {
	setupTenantTestDB(t)

	// Two users whose usernames slugify identically.
	a := createTestUser(t, "acme-inc", 0)
	b := createTestUser(t, "Acme Inc.", 0)

	ta, err := EnsureTenantForUser(a.Id)
	require.NoError(t, err)
	tb, err := EnsureTenantForUser(b.Id)
	require.NoError(t, err)

	assert.Equal(t, "acme-inc", ta.Slug)
	assert.NotEqual(t, ta.Slug, tb.Slug, "colliding slug must be suffixed")
	assert.Contains(t, tb.Slug, "acme-inc")
	assert.NotEqual(t, ta.Id, tb.Id)
}

func TestAddUserToTenantSharesOneBalance(t *testing.T) {
	setupTenantTestDB(t)

	owner := createTestUser(t, "owner", 1000)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)

	// A second human joins, bringing their own balance with them.
	member := createTestUser(t, "member", 250)
	require.NoError(t, AddUserToTenant(member.Id, tenant.Id))

	quota, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 1250, quota, "personal balance folds into the tenant")

	var reloaded User
	require.NoError(t, DB.First(&reloaded, member.Id).Error)
	assert.Equal(t, tenant.Id, reloaded.TenantId)
	assert.Equal(t, 0, reloaded.Quota)

	members, err := GetTenantMembers(tenant.Id)
	require.NoError(t, err)
	assert.Len(t, members, 2, "both humans share one billing entity")
}

func TestAddUserToTenantIsIdempotent(t *testing.T) {
	setupTenantTestDB(t)

	owner := createTestUser(t, "owner2", 500)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)

	// Re-adding the owner must not credit their (now zero) balance again, and
	// more importantly must not double-count if it were non-zero.
	require.NoError(t, AddUserToTenant(owner.Id, tenant.Id))

	quota, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)
}

func TestTenantsAreIsolatedFromEachOther(t *testing.T) {
	setupTenantTestDB(t)

	a := createTestUser(t, "tenant-a-user", 800)
	b := createTestUser(t, "tenant-b-user", 300)
	ta, err := EnsureTenantForUser(a.Id)
	require.NoError(t, err)
	tb, err := EnsureTenantForUser(b.Id)
	require.NoError(t, err)

	require.NoError(t, decreaseTenantQuota(ta.Id, 200))

	qa, err := GetTenantQuota(ta.Id)
	require.NoError(t, err)
	qb, err := GetTenantQuota(tb.Id)
	require.NoError(t, err)

	assert.Equal(t, 600, qa)
	assert.Equal(t, 300, qb, "spending in one tenant must not touch another")

	membersA, err := GetTenantMembers(ta.Id)
	require.NoError(t, err)
	assert.Len(t, membersA, 1)
	assert.Equal(t, a.Id, membersA[0].Id)
}

// An untenanted user must behave exactly as upstream does. This is the
// regression guard for the additive design: tenant_id == 0 changes nothing.
func TestUntenantedUserKeepsItsOwnBalance(t *testing.T) {
	setupTenantTestDB(t)
	user := createTestUser(t, "legacy", 4200)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, 0, reloaded.TenantId, "no tenant assigned")
	assert.Equal(t, 4200, reloaded.Quota, "balance stays on the user row")

	total, err := CountTenants()
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

// Operator accounts must never become tenants, or our own team shows up in the
// customer list that the operations view reports on.
func TestStaffAccountsGetNoTenant(t *testing.T) {
	setupTenantTestDB(t)

	for name, role := range map[string]int{
		"an-admin": common.RoleAdminUser,
		"the-root": common.RoleRootUser,
	} {
		user := createTestUser(t, name, 0)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("role", role).Error)

		tenant, err := EnsureTenantForUser(user.Id)
		require.NoError(t, err, "must not error, just decline")
		assert.Nil(t, tenant, "role %d must not get a tenant", role)

		var reloaded User
		require.NoError(t, DB.First(&reloaded, user.Id).Error)
		assert.Equal(t, 0, reloaded.TenantId)
	}

	total, err := CountTenants()
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

func TestStaffAccountsAreExcludedFromTheCustomerList(t *testing.T) {
	setupTenantTestDB(t)

	customer := createTestUser(t, "paying-customer", 1000)
	_, err := EnsureTenantForUser(customer.Id)
	require.NoError(t, err)

	ops := createTestUser(t, "ops-account", 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", ops.Id).Update("role", common.RoleRootUser).Error)
	_, err = EnsureTenantForUser(ops.Id)
	require.NoError(t, err)

	overviews, err := GetTenantOverviews(0, 0, 100, 0)
	require.NoError(t, err)
	require.Len(t, overviews, 1, "only the paying customer is a tenant")
	assert.Equal(t, "paying-customer", overviews[0].Slug)
	assert.Equal(t, 1, overviews[0].MemberCount)
}

func TestAddUserToTenantRefusesStaff(t *testing.T) {
	setupTenantTestDB(t)

	customer := createTestUser(t, "cust", 500)
	tenant, err := EnsureTenantForUser(customer.Id)
	require.NoError(t, err)

	ops := createTestUser(t, "ops", 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", ops.Id).Update("role", common.RoleAdminUser).Error)

	err = AddUserToTenant(ops.Id, tenant.Id)
	assert.Error(t, err, "an operator must not join a customer's billing entity")

	quota, qErr := GetTenantQuota(tenant.Id)
	require.NoError(t, qErr)
	assert.Equal(t, 500, quota, "balance untouched by the refused join")
}

func TestIsStaffRole(t *testing.T) {
	assert.False(t, IsStaffRole(common.RoleGuestUser))
	assert.False(t, IsStaffRole(common.RoleCommonUser))
	assert.True(t, IsStaffRole(common.RoleAdminUser))
	assert.True(t, IsStaffRole(common.RoleRootUser))
}

func TestTenantQuotaMutators(t *testing.T) {
	setupTenantTestDB(t)
	user := createTestUser(t, "spender", 1000)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	require.NoError(t, increaseTenantQuota(tenant.Id, 500))
	require.NoError(t, decreaseTenantQuota(tenant.Id, 200))
	require.NoError(t, increaseTenantUsedQuota(tenant.Id, 200))

	quota, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 1300, quota)

	reloaded, err := GetTenantById(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 200, reloaded.UsedQuota)
}

func TestGetTenantByIdAndSlug(t *testing.T) {
	setupTenantTestDB(t)
	user := createTestUser(t, "lookup", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	byId, err := GetTenantById(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, tenant.Id, byId.Id)

	bySlug, err := GetTenantBySlug(tenant.Slug)
	require.NoError(t, err)
	assert.Equal(t, tenant.Id, bySlug.Id)

	_, err = GetTenantById(0)
	assert.Error(t, err, "id 0 means no tenant and must not silently match")
	_, err = GetTenantBySlug("")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Operations reporting
// ---------------------------------------------------------------------------

func TestGetTenantOverviewsReportsMembershipBalanceAndSpend(t *testing.T) {
	setupTenantTestDB(t)

	owner := createTestUser(t, "acme", 1000)
	acme, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)
	colleague := createTestUser(t, "acme-colleague", 0)
	require.NoError(t, AddUserToTenant(colleague.Id, acme.Id))

	soloUser := createTestUser(t, "solo", 50)
	solo, err := EnsureTenantForUser(soloUser.Id)
	require.NoError(t, err)

	// Consume logs from both members of acme, plus one for solo, plus a
	// non-consume log that must be excluded from spend.
	logs := []Log{
		{UserId: owner.Id, Type: LogTypeConsume, Quota: 100, PromptTokens: 10, CompletionTokens: 5, ModelName: "gpt-5", CreatedAt: 1000},
		{UserId: colleague.Id, Type: LogTypeConsume, Quota: 40, PromptTokens: 4, CompletionTokens: 2, ModelName: "claude-opus-5", CreatedAt: 1500},
		{UserId: soloUser.Id, Type: LogTypeConsume, Quota: 7, PromptTokens: 1, CompletionTokens: 1, ModelName: "gpt-5", CreatedAt: 1200},
		{UserId: owner.Id, Type: LogTypeTopup, Quota: 99999, CreatedAt: 1300},
	}
	for i := range logs {
		require.NoError(t, DB.Create(&logs[i]).Error)
	}

	overviews, err := GetTenantOverviews(0, 0, 100, 0)
	require.NoError(t, err)
	require.Len(t, overviews, 2)

	byId := map[int]*TenantOverview{}
	for _, o := range overviews {
		byId[o.TenantId] = o
	}

	a := byId[acme.Id]
	require.NotNil(t, a)
	assert.Equal(t, 2, a.MemberCount)
	assert.Equal(t, 1000, a.Quota)
	assert.Equal(t, 140, a.PeriodQuota, "sums both members, excludes the topup log")
	assert.Equal(t, 2, a.PeriodRequests)
	assert.Equal(t, 14, a.PeriodPromptTokens)
	assert.Equal(t, 7, a.PeriodCompletionTokens)
	assert.Equal(t, int64(1500), a.LastActivityAt)

	s := byId[solo.Id]
	require.NotNil(t, s)
	assert.Equal(t, 1, s.MemberCount)
	assert.Equal(t, 7, s.PeriodQuota, "one tenant's spend must not leak into another")
}

func TestGetTenantOverviewsRespectsTimeWindow(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "windowed", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	for _, l := range []Log{
		{UserId: user.Id, Type: LogTypeConsume, Quota: 10, CreatedAt: 100},
		{UserId: user.Id, Type: LogTypeConsume, Quota: 20, CreatedAt: 500},
		{UserId: user.Id, Type: LogTypeConsume, Quota: 40, CreatedAt: 900},
	} {
		log := l
		require.NoError(t, DB.Create(&log).Error)
	}

	all, err := GetTenantOverviews(0, 0, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, 70, all[0].PeriodQuota)

	mid, err := GetTenantOverviews(200, 800, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, 20, mid[0].PeriodQuota, "window must bound the sum")
	assert.Equal(t, 1, mid[0].PeriodRequests)

	_ = tenant
}

func TestGetTenantOverviewsPaginates(t *testing.T) {
	setupTenantTestDB(t)
	for _, name := range []string{"t1", "t2", "t3"} {
		u := createTestUser(t, name, 0)
		_, err := EnsureTenantForUser(u.Id)
		require.NoError(t, err)
	}

	page1, err := GetTenantOverviews(0, 0, 2, 0)
	require.NoError(t, err)
	assert.Len(t, page1, 2)

	page2, err := GetTenantOverviews(0, 0, 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 1)

	assert.NotEqual(t, page1[0].TenantId, page2[0].TenantId)
}

func TestGetTenantOverviewsEmpty(t *testing.T) {
	setupTenantTestDB(t)
	rows, err := GetTenantOverviews(0, 0, 100, 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestGetTenantModelUsage(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "modeluser", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	other := createTestUser(t, "otheruser", 0)
	otherTenant, err := EnsureTenantForUser(other.Id)
	require.NoError(t, err)

	for _, l := range []Log{
		{UserId: user.Id, Type: LogTypeConsume, Quota: 100, ModelName: "gpt-5", PromptTokens: 10, CreatedAt: 100},
		{UserId: user.Id, Type: LogTypeConsume, Quota: 50, ModelName: "gpt-5", PromptTokens: 5, CreatedAt: 200},
		{UserId: user.Id, Type: LogTypeConsume, Quota: 300, ModelName: "claude-opus-5", PromptTokens: 30, CreatedAt: 300},
		{UserId: other.Id, Type: LogTypeConsume, Quota: 999, ModelName: "gpt-5", CreatedAt: 400},
	} {
		log := l
		require.NoError(t, DB.Create(&log).Error)
	}

	usage, err := GetTenantModelUsage(tenant.Id, 0, 0)
	require.NoError(t, err)
	require.Len(t, usage, 2)

	// Ordered by spend descending.
	assert.Equal(t, "claude-opus-5", usage[0].ModelName)
	assert.Equal(t, 300, usage[0].Quota)
	assert.Equal(t, 1, usage[0].Requests)

	assert.Equal(t, "gpt-5", usage[1].ModelName)
	assert.Equal(t, 150, usage[1].Quota, "aggregates repeat calls to one model")
	assert.Equal(t, 2, usage[1].Requests)
	assert.Equal(t, 15, usage[1].PromptTokens)

	_, err = GetTenantModelUsage(0, 0, 0)
	assert.Error(t, err)

	otherUsage, err := GetTenantModelUsage(otherTenant.Id, 0, 0)
	require.NoError(t, err)
	require.Len(t, otherUsage, 1)
	assert.Equal(t, 999, otherUsage[0].Quota)
}
