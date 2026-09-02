package model

// UNIFYAPI-FORK: first-class customer x model commercial contracts.
//
// A tenant is the company boundary. Each row records exactly one price and the
// dedicated upstream channels that may serve that company's requests for one
// model. This avoids encoding commercial contracts in user groups, and it
// prevents a retry from silently crossing onto another customer's API key.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
)

type TenantModelContract struct {
	Id        int     `json:"id" gorm:"primaryKey"`
	TenantId  int     `json:"tenant_id" gorm:"not null;uniqueIndex:ux_tenant_model_contract,priority:1;index"`
	Model     string  `json:"model" gorm:"type:varchar(255);not null;uniqueIndex:ux_tenant_model_contract,priority:2"`
	Discount  float64 `json:"discount" gorm:"type:decimal(12,8);not null"`
	Enabled   bool    `json:"enabled" gorm:"not null;default:true;index"`
	CreatedAt int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64   `json:"updated_at" gorm:"autoUpdateTime"`

	Channels []TenantModelChannel `json:"channels,omitempty" gorm:"foreignKey:ContractId;constraint:OnDelete:CASCADE"`
}

type TenantModelChannel struct {
	Id         int   `json:"id" gorm:"primaryKey"`
	ContractId int   `json:"contract_id" gorm:"not null;uniqueIndex:ux_tenant_model_channel,priority:1;index"`
	ChannelId  int   `json:"channel_id" gorm:"not null;uniqueIndex:ux_tenant_model_channel,priority:2;index"`
	CreatedAt  int64 `json:"created_at" gorm:"autoCreateTime"`
}

var (
	ErrTenantModelContractNotFound                = errors.New("tenant model contract not found")
	ErrStrictModelContractsRequireEnabledContract = errors.New("strict customer-model mode requires at least one enabled contract")
)

const tenantModelChannelOwnerIndex = "ux_tenant_model_channel_owner"

func migrateTenantModelContractTables() error {
	contractsExist := DB.Migrator().HasTable(&TenantModelContract{})
	channelsExist := DB.Migrator().HasTable(&TenantModelChannel{})
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) && contractsExist && channelsExist {
		// Do not send an existing decimal(12,8) + FK table through gorm's
		// SQLite DDL rewriter. The driver cannot parse that valid DDL reliably.
	} else {
		if contractsExist != channelsExist {
			return errors.New("partial tenant-model contract schema; both tables must exist or both be absent")
		}
		if err := DB.AutoMigrate(&TenantModelContract{}, &TenantModelChannel{}); err != nil {
			return err
		}
	}
	return ensureTenantModelContractIndexes()
}

func ensureTenantModelContractIndexes() error {
	indexes := []struct {
		model any
		name  string
		sql   string
	}{
		{&TenantModelContract{}, "ux_tenant_model_contract", "CREATE UNIQUE INDEX ux_tenant_model_contract ON tenant_model_contracts (tenant_id, model)"},
		{&TenantModelChannel{}, "ux_tenant_model_channel", "CREATE UNIQUE INDEX ux_tenant_model_channel ON tenant_model_channels (contract_id, channel_id)"},
	}
	for _, index := range indexes {
		if DB.Migrator().HasIndex(index.model, index.name) {
			continue
		}
		if err := DB.Exec(index.sql).Error; err != nil {
			return fmt.Errorf("failed to create %s: %w", index.name, err)
		}
	}
	return ensureTenantModelChannelOwnershipIndex()
}

// ensureTenantModelChannelOwnershipIndex is deliberately separate from the
// gorm field tag. Adding a second uniqueIndex tag to an existing SQLite table
// makes gorm rebuild the FK-bearing table, and its SQLite DDL parser cannot
// parse that legacy CREATE TABLE statement. A plain index is all we need and
// upgrades safely on SQLite, PostgreSQL and MySQL.
func ensureTenantModelChannelOwnershipIndex() error {
	var duplicate struct {
		ChannelId int
		Count     int64
	}
	result := DB.Model(&TenantModelChannel{}).
		Select("channel_id, COUNT(*) AS count").
		Group("channel_id").
		Having("COUNT(*) > 1").
		Limit(1).
		Scan(&duplicate)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return fmt.Errorf("channel %d is bound to %d customer-model contracts; resolve duplicate ownership before migration", duplicate.ChannelId, duplicate.Count)
	}
	if DB.Migrator().HasIndex(&TenantModelChannel{}, tenantModelChannelOwnerIndex) {
		return nil
	}
	return DB.Exec("CREATE UNIQUE INDEX " + tenantModelChannelOwnerIndex + " ON tenant_model_channels (channel_id)").Error
}

func GetTenantModelContract(tenantId int, modelName string, enabledOnly bool) (*TenantModelContract, error) {
	if tenantId <= 0 || strings.TrimSpace(modelName) == "" {
		return nil, ErrTenantModelContractNotFound
	}
	query := DB.Preload("Channels").Where("tenant_id = ? AND model = ?", tenantId, modelName)
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	var contract TenantModelContract
	result := query.Limit(1).Find(&contract)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrTenantModelContractNotFound
	}
	return &contract, nil
}

func ListTenantModelContracts() ([]TenantModelContract, error) {
	var contracts []TenantModelContract
	err := DB.Preload("Channels").Order("tenant_id ASC, model ASC").Find(&contracts).Error
	return contracts, err
}

func ListEnabledTenantModelContractsForTenant(tenantId int) ([]TenantModelContract, error) {
	if tenantId <= 0 {
		return []TenantModelContract{}, nil
	}
	var contracts []TenantModelContract
	err := DB.Where("tenant_id = ? AND enabled = ?", tenantId, true).
		Order("model ASC").Find(&contracts).Error
	return contracts, err
}

func UpsertTenantModelContract(contract *TenantModelContract, channelIds []int) error {
	if contract == nil {
		return errors.New("contract is required")
	}
	channelIds = uniquePositiveInts(channelIds)
	return DB.Transaction(func(tx *gorm.DB) error {
		var existing TenantModelContract
		err := tx.Where("tenant_id = ? AND model = ?", contract.TenantId, contract.Model).First(&existing).Error
		switch {
		case err == nil:
			contract.Id = existing.Id
			if err := tx.Model(&existing).Updates(map[string]any{
				"discount": contract.Discount,
				"enabled":  contract.Enabled,
			}).Error; err != nil {
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(contract).Error; err != nil {
				return err
			}
		default:
			return err
		}

		if err := tx.Where("contract_id = ?", contract.Id).Delete(&TenantModelChannel{}).Error; err != nil {
			return err
		}
		bindings := make([]TenantModelChannel, 0, len(channelIds))
		for _, channelId := range channelIds {
			bindings = append(bindings, TenantModelChannel{ContractId: contract.Id, ChannelId: channelId})
		}
		if len(bindings) > 0 {
			if err := tx.Create(&bindings).Error; err != nil {
				return err
			}
		}
		if err := ensureStrictModelContractsRemainUsable(tx, contract.TenantId); err != nil {
			return err
		}
		contract.Channels = bindings
		return nil
	})
}

func DeleteTenantModelContract(id int) error {
	if id <= 0 {
		return errors.New("invalid contract id")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var contract TenantModelContract
		if err := tx.First(&contract, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTenantModelContractNotFound
			}
			return err
		}
		strict, err := tenantRequiresStrictModelContractsTx(tx, contract.TenantId)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// The controller never permits this state, but older imports and
			// low-level callers can carry an unowned draft contract. Preserve
			// their historical behavior; strict-mode protection applies only to
			// an actual customer company.
			err = nil
			strict = false
		}
		if err != nil {
			return err
		}
		if strict && contract.Enabled {
			var enabled int64
			if err := tx.Model(&TenantModelContract{}).
				Where("tenant_id = ? AND enabled = ?", contract.TenantId, true).
				Count(&enabled).Error; err != nil {
				return err
			}
			if enabled <= 1 {
				return ErrStrictModelContractsRequireEnabledContract
			}
		}
		// Delete explicitly as well as carrying an FK cascade. SQLite commonly
		// runs with foreign_keys off in tests, and no environment should retain
		// orphaned channel bindings merely because that switch differs.
		if err := tx.Where("contract_id = ?", id).Delete(&TenantModelChannel{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&TenantModelContract{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTenantModelContractNotFound
		}
		return nil
	})
}

// ensureStrictModelContractsRemainUsable keeps the strict-mode toggle from
// becoming a lockout switch. It is enforced inside the mutation transaction,
// not only in the browser, so scripts and concurrent admin requests receive
// the same protection.
func ensureStrictModelContractsRemainUsable(tx *gorm.DB, tenantId int) error {
	strict, err := tenantRequiresStrictModelContractsTx(tx, tenantId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Tenant existence is enforced at the HTTP boundary. Do not turn a
		// legacy/import draft into a failed write merely because it has no
		// company record to place in strict mode.
		return nil
	}
	if err != nil || !strict {
		return err
	}
	var enabled int64
	if err := tx.Model(&TenantModelContract{}).
		Where("tenant_id = ? AND enabled = ?", tenantId, true).
		Count(&enabled).Error; err != nil {
		return err
	}
	if enabled == 0 {
		return ErrStrictModelContractsRequireEnabledContract
	}
	return nil
}

func tenantRequiresStrictModelContractsTx(tx *gorm.DB, tenantId int) (bool, error) {
	if tenantId <= 0 {
		return false, nil
	}
	var strict bool
	result := tx.Model(&Tenant{}).Where("id = ?", tenantId).
		Select("strict_model_contracts").Scan(&strict)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, gorm.ErrRecordNotFound
	}
	return strict, nil
}

func TenantModelContractContainsChannel(contract *TenantModelContract, channelId int) bool {
	if contract == nil || channelId <= 0 {
		return false
	}
	for _, binding := range contract.Channels {
		if binding.ChannelId == channelId {
			return true
		}
	}
	return false
}

// GetTenantModelContractChannel selects only among channels explicitly bound
// to this contract. A channel is eligible only while enabled and while its
// model list still contains exactly the contracted model. Configuration drift
// therefore fails closed instead of leaking traffic to a shared key.
func GetTenantModelContractChannel(contract *TenantModelContract, retry int, requestPath string) (*Channel, error) {
	if contract == nil || !contract.Enabled || len(contract.Channels) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(contract.Channels))
	for _, binding := range contract.Channels {
		ids = append(ids, binding.ChannelId)
	}

	var channels []*Channel
	if err := DB.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	eligible := make([]*Channel, 0, len(channels))
	for _, channel := range channels {
		if !ChannelCarriesOnlyModel(channel, contract.Model) {
			continue
		}
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			config := channel.GetOtherSettings().AdvancedCustom
			if config == nil || !config.SupportsPathForModel(requestPath, contract.Model) {
				continue
			}
		}
		eligible = append(eligible, channel)
	}
	if len(eligible) == 0 {
		return nil, nil
	}

	priorities := make([]int64, 0)
	seenPriorities := map[int64]struct{}{}
	for _, channel := range eligible {
		priority := int64(0)
		if channel.Priority != nil {
			priority = *channel.Priority
		}
		if _, seen := seenPriorities[priority]; !seen {
			seenPriorities[priority] = struct{}{}
			priorities = append(priorities, priority)
		}
	}
	sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
	priorityIndex := retry
	if priorityIndex < 0 {
		priorityIndex = 0
	}
	if priorityIndex >= len(priorities) {
		priorityIndex = len(priorities) - 1
	}
	wantedPriority := priorities[priorityIndex]

	candidates := make([]*Channel, 0, len(eligible))
	weightSum := 0
	for _, channel := range eligible {
		priority := int64(0)
		if channel.Priority != nil {
			priority = *channel.Priority
		}
		if priority == wantedPriority {
			candidates = append(candidates, channel)
			weightSum += channel.GetWeight() + 10
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no eligible channel at priority %d", wantedPriority)
	}
	pick := common.GetRandomInt(weightSum)
	for _, channel := range candidates {
		pick -= channel.GetWeight() + 10
		if pick <= 0 {
			return GetChannelById(channel.Id, true)
		}
	}
	return GetChannelById(candidates[len(candidates)-1].Id, true)
}

func ChannelCarriesOnlyModel(channel *Channel, modelName string) bool {
	if channel == nil {
		return false
	}
	models := make([]string, 0, 1)
	for _, raw := range strings.Split(channel.Models, ",") {
		if model := strings.TrimSpace(raw); model != "" {
			models = append(models, model)
		}
	}
	return len(models) == 1 && models[0] == modelName
}

func ListTenantModelContractTenants() ([]Tenant, error) {
	var tenants []Tenant
	err := DB.Select("id", "name", "slug", "status", "strict_model_contracts").
		Order("name ASC, id ASC").Find(&tenants).Error
	return tenants, err
}

// ListTenantModelContractChannels returns no credentials. Contract editing only
// needs enough metadata to identify and validate a dedicated route.
func ListTenantModelContractChannels() ([]Channel, error) {
	var channels []Channel
	err := DB.Omit("key").Order("name ASC, id ASC").Find(&channels).Error
	return channels, err
}

func GetChannelsForTenantModelContract(ids []int) ([]Channel, error) {
	ids = uniquePositiveInts(ids)
	if len(ids) == 0 {
		return []Channel{}, nil
	}
	var channels []Channel
	err := DB.Where("id IN ?", ids).Find(&channels).Error
	return channels, err
}

func GetTenantModelChannelBindings(channelIds []int) ([]TenantModelChannel, error) {
	channelIds = uniquePositiveInts(channelIds)
	if len(channelIds) == 0 {
		return []TenantModelChannel{}, nil
	}
	var bindings []TenantModelChannel
	err := DB.Where("channel_id IN ?", channelIds).Find(&bindings).Error
	return bindings, err
}

func uniquePositiveInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}
