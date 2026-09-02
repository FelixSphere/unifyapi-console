package ratio_setting

// UNIFYAPI-FORK: final customer-group x model multipliers.
//
// GroupRatio is useful for a broad tier and ModelDiscount is useful for a
// broad model promotion, but multiplying both cannot express a negotiated
// contract such as "GenAI pays 0.8x for Opus 4 and 0.9x for Opus 5" without
// surprising stacking. A value in this table is therefore the FINAL
// multiplier over the official catalog price. It replaces (rather than
// multiplies) ModelDiscount x GroupRatio for that user group and model.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

const maxGroupModelDiscount = 10.0

var groupModelDiscountMap = types.NewRWMap[string, map[string]float64]()

func GroupModelDiscount2JSONString() string {
	return groupModelDiscountMap.MarshalJSONString()
}

func GetGroupModelDiscount(userGroup, model string) (float64, bool) {
	model = FormatMatchingModelName(model)
	models, ok := groupModelDiscountMap.Get(userGroup)
	if !ok {
		return 0, false
	}
	ratio, ok := models[model]
	return ratio, ok && ratio > 0
}

// GetGroupModelDiscountCopy returns a deep copy. The RWMap protects the outer
// map, but its nested maps are ordinary maps and must never escape to callers
// that merge an admin edit into the current table.
func GetGroupModelDiscountCopy() map[string]map[string]float64 {
	snapshot := groupModelDiscountMap.ReadAll()
	out := make(map[string]map[string]float64, len(snapshot))
	for group, models := range snapshot {
		copied := make(map[string]float64, len(models))
		for model, ratio := range models {
			copied[model] = ratio
		}
		out[group] = copied
	}
	return out
}

func UpdateGroupModelDiscountByJSONString(jsonStr string) error {
	var incoming map[string]map[string]float64
	if err := common.Unmarshal([]byte(jsonStr), &incoming); err != nil {
		return err
	}
	if incoming == nil {
		incoming = map[string]map[string]float64{}
	}
	if problems := ValidateGroupModelDiscounts(incoming); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		return fmt.Errorf("拒绝保存客户模型价格：%s", strings.Join(messages, "; "))
	}
	return types.LoadFromJsonString(groupModelDiscountMap, jsonStr)
}

func ValidateGroupModelDiscounts(discounts map[string]map[string]float64) []error {
	groups := make([]string, 0, len(discounts))
	for group := range discounts {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	var problems []error
	for _, group := range groups {
		if strings.TrimSpace(group) == "" {
			problems = append(problems, fmt.Errorf("customer group must not be empty"))
			continue
		}
		models := make([]string, 0, len(discounts[group]))
		for model := range discounts[group] {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			ratio := discounts[group][model]
			if _, ok := CatalogEntryFor(model); !ok {
				problems = append(problems, fmt.Errorf("%s / %s: model has no official catalog price", group, model))
				continue
			}
			switch {
			case ratio <= 0:
				problems = append(problems, fmt.Errorf("%s / %s: multiplier must be greater than 0", group, model))
			case ratio > maxGroupModelDiscount:
				problems = append(problems, fmt.Errorf("%s / %s: multiplier %g exceeds the sanity bound of %g", group, model, ratio, maxGroupModelDiscount))
			}
		}
	}
	return problems
}
