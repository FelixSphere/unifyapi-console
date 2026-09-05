# 2026-08-31 折扣变更：DeepSeek 系列 9 折 → 7 折

## 要改什么

`ModelDiscount` 这一行 options，整表替换成 `model-discount-2026-08-31.json` 里的 `discounts`。

**46 条，一条都不能少。** `types.LoadFromJsonString` 是替换语义——少写一个模型，那个模型的折扣就没了，它会立刻按官方价卖。2026-08-29 丢掉整张折扣表就是这么丢的。

## 差异

只有 DeepSeek 家族 5 个从 `0.9` 变成 `0.7`，其余 41 条原样保留：

| 模型 | 官方价 $/1M（入+出） | 现在 9 折 | 改后 7 折 |
|---|---:|---:|---:|
| `deepseek-v4-flash` | 0.44 + 1.32 | $1.584 | **$1.232** |
| `deepseek-v4-pro` | 1.32 + 3.96 | $4.752 | **$3.696** |
| `deepseek-v3` | 0.287 + 1.147 | $1.291 | **$1.004** |
| `deepseek-v3.2` | 0.18 + 0.35 | $0.477 | **$0.371** |
| `deepseek-v3.2-thinking` | 0.29 + 0.43 | $0.648 | **$0.504** |

对客户是**降价 22.2%**（0.7 ÷ 0.9），不是涨价。

## 怎么应用

走后台或接口，**不要用 SQL**：

```
PUT /api/pricing/discount     body = model-discount-2026-08-31.json 的内容
```

或后台：**计费与支付 → 模型定价 → 官方报价与折扣**。

两条路都会经过 `model.UpdateOption`，因此**前一版会被自动快照留底**（`pricing_config_histories`）。裸 SQL 不会，那正是 08-29 那次无从恢复的原因。

## 应用后怎么验

```bash
curl -s https://app.unifyapi.ai/api/pricing | python3 -c "
import sys,json
r={m['model_name']:m for m in json.load(sys.stdin)['data']}
print('deepseek-v4-pro ratio =', r['deepseek-v4-pro']['model_ratio'], '（应为 0.462 = 1.32/2*0.7）')
print('claude-opus-4-8 ratio =', r['claude-opus-4-8']['model_ratio'], '（应仍为 2.25，未受影响）')"
```

第二行是对照组：它证明这次改动没有波及 DeepSeek 以外的模型。

## 一个必须一起看的数

生产上 `ChannelCostRatio` **是空的**，也就是采购成本按厂商官方价计。在那个前提下，7 折 = 按成本的 70% 卖，对账页会把 DeepSeek 全部标成亏损。

这不一定是真的亏——取决于你和 DeepSeek 实际谈下来的采购折扣。**但在采购倍率填进去之前，DeepSeek 的毛利数字是没有意义的。** 见 `PRICING-AND-DISCOUNTS.md` 的「上游采购成本」一节。
