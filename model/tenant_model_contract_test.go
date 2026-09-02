package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTenantModelContractTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&Tenant{}, &Channel{}, &TenantModelContract{}, &TenantModelChannel{},
	))
	previous := DB
	DB = db
	require.NoError(t, ensureTenantModelChannelOwnershipIndex())
	t.Cleanup(func() { DB = previous })
}

func TestTenantModelContractMigrationUpgradesExistingSQLiteTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE tenant_model_contracts (
		id integer PRIMARY KEY, tenant_id integer NOT NULL, model varchar(255) NOT NULL,
		discount decimal(12,8) NOT NULL, enabled numeric NOT NULL DEFAULT true,
		created_at integer, updated_at integer
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE tenant_model_channels (
		id integer PRIMARY KEY, contract_id integer NOT NULL, channel_id integer NOT NULL,
		created_at integer, CONSTRAINT fk_tenant_model_contracts_channels
		FOREIGN KEY (contract_id) REFERENCES tenant_model_contracts(id) ON DELETE CASCADE
	)`).Error)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	require.NoError(t, migrateTenantModelContractTables())
	require.True(t, DB.Migrator().HasIndex(&TenantModelChannel{}, tenantModelChannelOwnerIndex))
	require.True(t, DB.Migrator().HasIndex(&TenantModelContract{}, "ux_tenant_model_contract"))
}

func testPriority(value int64) *int64 { return &value }

func createContractChannel(t *testing.T, name, models string, status int, priority int64) Channel {
	t.Helper()
	channel := Channel{
		Name:     name,
		Key:      "test-only",
		Models:   models,
		Status:   status,
		Priority: testPriority(priority),
	}
	require.NoError(t, DB.Create(&channel).Error)
	return channel
}

func TestTenantModelContractUpsertReplacesBindingsAtomically(t *testing.T) {
	setupTenantModelContractTestDB(t)
	tenant := Tenant{Name: "Acme", Slug: "acme", Status: TenantStatusEnabled}
	require.NoError(t, DB.Create(&tenant).Error)
	a := createContractChannel(t, "acme opus primary", "claude-opus-5", common.ChannelStatusEnabled, 10)
	b := createContractChannel(t, "acme opus backup", "claude-opus-5", common.ChannelStatusEnabled, 0)

	contract := &TenantModelContract{TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.8, Enabled: true}
	require.NoError(t, UpsertTenantModelContract(contract, []int{a.Id}))
	firstID := contract.Id
	require.Positive(t, firstID)

	contract.Discount = 0.7
	require.NoError(t, UpsertTenantModelContract(contract, []int{b.Id, b.Id}))
	require.Equal(t, firstID, contract.Id, "tenant + model must update, never duplicate")

	loaded, err := GetTenantModelContract(tenant.Id, "claude-opus-5", true)
	require.NoError(t, err)
	require.InDelta(t, 0.7, loaded.Discount, 1e-9)
	require.Len(t, loaded.Channels, 1)
	require.Equal(t, b.Id, loaded.Channels[0].ChannelId)
}

func TestTenantModelContractsAreScopedByCompanyAndModel(t *testing.T) {
	setupTenantModelContractTestDB(t)
	acme := Tenant{Name: "Acme", Slug: "acme", Status: TenantStatusEnabled}
	globex := Tenant{Name: "Globex", Slug: "globex", Status: TenantStatusEnabled}
	require.NoError(t, DB.Create(&acme).Error)
	require.NoError(t, DB.Create(&globex).Error)

	require.NoError(t, UpsertTenantModelContract(&TenantModelContract{
		TenantId: acme.Id, Model: "claude-opus-5", Discount: 0.7, Enabled: true,
	}, nil))
	require.NoError(t, UpsertTenantModelContract(&TenantModelContract{
		TenantId: acme.Id, Model: "claude-fable-5.1", Discount: 0.85, Enabled: true,
	}, nil))
	require.NoError(t, UpsertTenantModelContract(&TenantModelContract{
		TenantId: globex.Id, Model: "claude-opus-5", Discount: 0.6, Enabled: true,
	}, nil))

	acmeOpus, err := GetTenantModelContract(acme.Id, "claude-opus-5", true)
	require.NoError(t, err)
	acmeFable, err := GetTenantModelContract(acme.Id, "claude-fable-5.1", true)
	require.NoError(t, err)
	globexOpus, err := GetTenantModelContract(globex.Id, "claude-opus-5", true)
	require.NoError(t, err)
	require.InDelta(t, 0.7, acmeOpus.Discount, 1e-9)
	require.InDelta(t, 0.85, acmeFable.Discount, 1e-9)
	require.InDelta(t, 0.6, globexOpus.Discount, 1e-9)
}

func TestDedicatedChannelCanBelongToOnlyOneContract(t *testing.T) {
	setupTenantModelContractTestDB(t)
	channel := createContractChannel(t, "acme opus", "claude-opus-5", common.ChannelStatusEnabled, 0)
	first := &TenantModelContract{TenantId: 1, Model: "claude-opus-5", Discount: 0.7, Enabled: true}
	second := &TenantModelContract{TenantId: 2, Model: "claude-opus-5", Discount: 0.6, Enabled: true}
	require.NoError(t, UpsertTenantModelContract(first, []int{channel.Id}))
	require.Error(t, UpsertTenantModelContract(second, []int{channel.Id}), "an upstream key must never be shared across company contracts")
	_, err := GetTenantModelContract(second.TenantId, second.Model, false)
	require.ErrorIs(t, err, ErrTenantModelContractNotFound, "the failed binding must roll back the contract row too")
}

func TestTenantModelContractRoutingHonorsPriorityAndFailsClosedOnDrift(t *testing.T) {
	setupTenantModelContractTestDB(t)
	primary := createContractChannel(t, "primary", "claude-opus-5", common.ChannelStatusEnabled, 10)
	backup := createContractChannel(t, "backup", "claude-opus-5", common.ChannelStatusEnabled, 0)
	shared := createContractChannel(t, "unsafe shared", "claude-opus-5,claude-fable-5.1", common.ChannelStatusEnabled, 99)
	contract := &TenantModelContract{TenantId: 1, Model: "claude-opus-5", Discount: 0.8, Enabled: true}
	require.NoError(t, UpsertTenantModelContract(contract, []int{primary.Id, backup.Id, shared.Id}))

	selected, err := GetTenantModelContractChannel(contract, 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, primary.Id, selected.Id, "multi-model channel is ineligible even at higher priority")

	selected, err = GetTenantModelContractChannel(contract, 1, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, backup.Id, selected.Id)

	require.NoError(t, DB.Model(&Channel{}).Where("id IN ?", []int{primary.Id, backup.Id}).
		Update("models", "claude-opus-5,claude-fable-5.1").Error)
	selected, err = GetTenantModelContractChannel(contract, 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.Nil(t, selected, "configuration drift must fail closed, not use a shared channel")
}

func TestDeletingContractCascadesBindings(t *testing.T) {
	setupTenantModelContractTestDB(t)
	channel := createContractChannel(t, "only", "gpt-4o", common.ChannelStatusEnabled, 0)
	contract := &TenantModelContract{TenantId: 1, Model: "gpt-4o", Discount: 0.9, Enabled: true}
	require.NoError(t, UpsertTenantModelContract(contract, []int{channel.Id}))
	require.NoError(t, DeleteTenantModelContract(contract.Id))

	var bindings int64
	require.NoError(t, DB.Model(&TenantModelChannel{}).Where("contract_id = ?", contract.Id).Count(&bindings).Error)
	require.Zero(t, bindings)
}

func TestStrictContractModeCannotLoseItsLastEnabledContract(t *testing.T) {
	setupTenantModelContractTestDB(t)
	tenant := Tenant{Name: "Acme", Slug: "acme", Status: TenantStatusEnabled, StrictModelContracts: true}
	require.NoError(t, DB.Create(&tenant).Error)
	contract := &TenantModelContract{TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.8, Enabled: true}
	require.NoError(t, UpsertTenantModelContract(contract, nil))

	contract.Enabled = false
	require.ErrorIs(t, UpsertTenantModelContract(contract, nil), ErrStrictModelContractsRequireEnabledContract)
	require.ErrorIs(t, DeleteTenantModelContract(contract.Id), ErrStrictModelContractsRequireEnabledContract)

	require.NoError(t, SetTenantStrictModelContracts(tenant.Id, false))
	require.NoError(t, DeleteTenantModelContract(contract.Id))
}
