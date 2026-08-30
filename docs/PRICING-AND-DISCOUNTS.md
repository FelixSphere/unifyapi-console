# 定价、折扣与对账

UnifyAPI 的定价只有三个价格。**每个价格只有一个含义，存在一个地方。**
把任意两个混进同一个数里，就是这套系统上次烂掉的原因。

| # | 价格 | 含义 | 存在哪 | 谁改 |
|---|---|---|---|---|
| 1 | **官方报价** | 厂商公开的 list price，USD/1M tokens | `setting/ratio_setting/unifyapi_catalog.go`（代码） | 提 PR，CI 对着 models.dev 校验 |
| 2 | **上游成本** | 我们的渠道实际收我们多少 | `ChannelCostRatio` option | admin，**仅用于对账** |
| 3 | **客户价** | 客户实付 | `ModelDiscount` × `GroupRatio` option | admin，改完立即生效 |

```
客户价   = 官方报价 × 模型折扣 × 分组折扣
上游成本 = 官方报价 × 渠道成本倍率
毛利     = 客户价 − 上游成本
```

---

## 为什么基线在代码里，不在数据库里

new-api 启动顺序（`main.go`）：

```
1. ratio_setting.InitRatioSettings()   ← 用 catalog 播种倍率
2. model.InitOptionMap()               ← 快照给后台 UI 显示
3. loadOptionsFromDatabase()           ← 用 options 表覆盖
```

第 3 步走 `types.LoadFromJsonString`，第一行就是 `m.data = make(map[K]V)`——**整表替换，不是合并**。
所以数据库里只要存在一行 `ModelRatio`，代码里整张基线表**一条都不生效**。

这就是 `claude-opus-4-8` 能以官方价的 8.5% 卖出去好几周的原因：数字在数据库里，没有任何检查能碰到它。

**所以那四行必须从数据库里删掉：**

```bash
psql "$SQL_DSN" -f seed-pricing.sql
```

删的是 `ModelRatio` `CompletionRatio` `CacheRatio` `CreateCacheRatio`。
删完重启容器（options 在启动时缓存进内存）。删完之后：

- 改价 = 一个经过 review 的 commit
- `scripts/pricing-drift` 能对着 models.dev 验证基线
- catalog 同时是**白名单**——不在 catalog 里的模型没有倍率，`GetModelRatio` 返回失败，relay 直接拒绝

> ⚠️ **不要再动后台「模型定价」页的保存按钮。**
> 按一下就会重新写出 `ModelRatio` 行，代码基线再次全表失效。
> 真按了也不会无声无息：启动日志会报 `PRICING: N model/ratio pairs are billing at...`，
> 后台「官方报价与折扣」tab 顶部会出现红色告警，`GET /api/pricing/baseline` 的 `shadows` 里能看到逐条差异。

---

## 商业价格就是官方报价（2026-08-30 决定）

**当前的商业定价 = 厂商官方标价，底下没有折扣层。** `ModelDiscount` 是空的，
生产的三个分组倍率都是 `1`，所以客户价 = 官方价。

这不是默认值，是一个明确的决定，背景值得记下来：

2026-08-29 02:33 生产上了 `unifyapi-v0.1.7` 并执行了 `seed-pricing.sql`，
基线切到官方报价。当时还有一个配套的 `seed-model-discount-neutral.sql`——它把每个
模型的折扣设成 `线上倍率 ÷ 官方倍率`，好让这次切换在账单上是 no-op——那个文件只
存在于未合并的分支上，所以没有被执行。

后来复盘发现，**不执行它反而是对的**：

1. 那张表是用除法从线上倍率反算出来的，**分不清折扣和错误**。`claude-opus-4-8`
   在里面是 `0.085`，而 `unifyapi_catalog.go` 的文件头写着这是把 `2.5` 打成
   `0.2125` 的小数点错位。恢复「中性」等于把这个 typo 一起装回去，而且它占 54%
   的流量。旁证：官方价完全相同的 `claude-opus-5` 根本不在那张表里，一直按标价卖。
2. 剩下 29 个折扣是真的定价决定，但按任何合理的采购倍率算，其中 18–24 个是**亏本卖**
   （盈亏线：折扣低于采购倍率就是亏）。
3. 那段时间的流量是测试流量，没有正式运营，所以没有需要补偿的账。

于是 `seed-model-discount-neutral.sql` 被删掉了。留着它就是留一把上了膛的枪：
跑一次就同时撤销了这个决定、并重新引入那个 typo。

**要给某个客户打折还是照常做**——见下面第一节，`ModelDiscount` 机制本身没有变，
只是现在是空的。区别在于以后每一个折扣都是有人主动填进去的，而不是从一堆线上倍率
里反算出来、混着历史错误的。

### 目录就是当前官方报价

`scripts/pricing-drift` 会拉 models.dev 和目录对差。2026-08-30 跑的结果：
**54 个模型无漂移**，其中 5 个在 models.dev 上没有条目，需要人工问厂商要报价：

```
deepseek-v3   deepseek-v3.2   deepseek-v3.2-thinking   glm-5-turbo   qwen3.5-flash
```

这 5 个带的是写表当时的价格，漂移检查每次都会把它们报出来，不会烂在那里没人知道。

## 一、给客户打折（最常见的操作）

### 按模型打折

**后台 → 系统设置 → 计费与支付 → 模型定价 → 「官方报价与折扣」tab**（第一个 tab）

页面每行一个模型：

| 列 | 可改 | 说明 |
|---|---|---|
| 模型 / 供应商 | ✗ | `未核验` 徽章 = models.dev 上查不到官方价，需要人工询价 |
| 官方价 入/出 | ✗ | 厂商官方价，USD/1M。要改得提 PR 改 catalog |
| **优惠** | ✓ | 乘数。`0.8` = 打八折；留空 = 按官方价卖；`>1` 会标黄并提示"加价 N%" |
| 各分组列 | ✗ | 实时算出的客户实付价 = 官方价 × 折扣 × 该分组倍率，**不用保存就能看到** |

改完点「保存折扣」。上方筛选框可按模型名或供应商过滤。

- 只有**偏离官方价的模型**会被存进数据库。留空/填 1 的模型不入库——所以那张表读起来就是"例外清单"，review 的时候一眼能看完。
- 「清除全部折扣」把所有模型恢复成官方价。
- 保存会被校验：给不在 catalog 里的模型打折、填 0、填负数、填超过 10 倍，都会拒绝并逐个告诉你是哪个模型。
- **折扣不能深于该厂商的采购折扣**，否则每个请求都在亏钱。上限在「上游采购成本」tab 的"客户折扣下限"列，见第二节。

命令行等价操作：

```bash
curl -s -X PUT "$CONSOLE/api/pricing/discount" \
  -H "Authorization: Bearer $ROOT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"discounts":{"claude-opus-5":0.85,"gpt-4o":0.9}}'
```

### 按客户打折

按模型折扣是**全站生效**的。要给特定客户打折，用分组：

1. **系统设置 → 计费 → 分组定价 → 分组倍率**：给分组设倍率。
   现有：`Standard User` = 1.0、`Premium User` = 0.9、`Vip User` = 0.8
2. 新建一个分组（比如 `Enterprise-Acme` = 0.7），在 `UserUsableGroups` 里授权
3. **用户页**把该客户的分组改成它

想给"某个客户买某个分组"再加一层特价，用 `GroupGroupRatio`（分组特殊倍率）。

### 两层折扣是相乘的

`claude-opus-5` 官方 $5/1M 输入：

| 模型折扣 | 分组 | 分组倍率 | 客户实付 |
|---|---|---|---|
| — | Standard | 1.0 | $5.00 |
| 0.9 | Standard | 1.0 | $4.50 |
| 0.9 | Vip | 0.8 | **$3.60** |
| 0.9 | Enterprise-Acme | 0.7 | **$3.15** |

**折扣自动作用于输出和缓存。** `completion_ratio` / `cache_ratio` 是**相对 model_ratio 的乘数**，
所以打八折是输入、输出、缓存读、缓存写一起八折——这才是"这个模型打八折"应有的含义。

---

## 二、配上游采购成本（对账用）

**只影响对账报表，绝不影响客户账单。**

原因是路由做负载均衡：同一个请求今天走 A 渠道、明天走 B 渠道。
如果渠道成本进了客户账单，同样的请求价格会跳变，客户自己没法对自己的账。

**后台 → 系统设置 → 计费与支付 → 模型定价 → 「上游采购成本」tab**（第二个 tab）

一行一个渠道：

| 列 | 可改 | 说明 |
|---|---|---|
| 渠道 | ✗ | `#id 名称`；禁用的渠道置灰 |
| 供应商 | ✗ | **从该渠道提供的模型自动推导**——你不用记渠道 id 对应哪家合同 |
| 模型数 | ✗ | 红色徽章 = 该渠道有价目表之外的模型，这部分**算不出成本**，鼠标悬停看是哪些。该渠道的毛利会被高估 |
| **采购倍率** | ✓ | 官方价的倍数。`0.7` = 采购价是官方价的七折；留空 = 按官方原价采购 |
| 采购折扣 | ✗ | 换算成人话。**填了大于 1 的值会标红**——那意味着我们付得比厂商公开价还高，几乎总是填错 |
| 客户折扣下限 | ✗ | 见下 |

改完点「保存采购成本」。只有偏离官方价的渠道会入库，清空即撤销。

### 「客户折扣下限」是什么

它等于采购倍率本身，这不是巧合：

**按官方价卖 + 按官方价采购 = 毛利恰好为零。** 所以毛利 100% 来自低于官方价的采购。
一个模型的客户折扣**只要深于该厂商的采购折扣，每一个请求都在亏钱**。

Anthropic 采购 0.65 → 该厂商模型的客户折扣不能低于 0.65。这一列就是把这个约束放在你填数字的地方，
而不是让你一个月后在对账报表里发现。

命令行等价操作：

```bash
curl -s "$CONSOLE/api/pricing/channel_cost" -H "Authorization: Bearer $ROOT_TOKEN"
curl -s -X PUT "$CONSOLE/api/pricing/channel_cost" \
  -H "Authorization: Bearer $ROOT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"cost_ratios":{"1":0.65,"2":0.8}}'
```

没配的渠道按 **1.0（即官方原价）** 计算成本。这是保守方向——只会低估毛利，不会高估。
校验会拒绝 0、超过 5 倍、以及用渠道名而不是 id 作为 key。

---

## 三、对账

### 收入侧 vs 成本侧的可信度不同

这两侧是**故意不对称**的：

- **收入是从账本读的。** 每条消费日志带着实际扣掉的 quota，里面已经含了模型折扣、分组倍率、缓存折扣、阶梯规则和限幅。一个数都不重算——所以收入这一列不可能和客户被扣的钱不一致。这是它能用来处理账单纠纷的唯一前提。
- **成本是建模的。** 厂商不会给我们逐请求的收据，所以只能用 `tokens × 官方价 × 渠道成本倍率` 算。这一列是估算，而这件事的意义就在于**拿它去和厂商实际发票对差**：差得小说明模型可信，差得大就是一个发现。

### 常用查询

```bash
# 按模型看毛利（最常用）
curl -s "$CONSOLE/api/pricing/reconcile?start=2026-08-01&end=2026-08-31&group_by=model" \
  -H "Authorization: Bearer $ROOT_TOKEN" | jq '.data.lines[] | {label,revenue_usd,cost_usd,margin_pct}'

# 亏钱的模型
... | jq '.data.loss_makers'

# 按客户 / 按渠道 / 按天 / 按厂商
group_by=customer | channel | day | vendor | group

# 导 CSV 给财务
curl -s "$CONSOLE/api/pricing/reconcile.csv?start=2026-08-01&end=2026-08-31&group_by=customer" \
  -H "Authorization: Bearer $ROOT_TOKEN" -o aug-by-customer.csv
```

### 和厂商发票对差

```bash
curl -s "$CONSOLE/api/pricing/reconcile?start=2026-08-01&end=2026-08-31&group_by=vendor\
&invoiced=anthropic:12405.50,openai:3102.20,google:880.00" \
  -H "Authorization: Bearer $ROOT_TOKEN" | jq '.variances'
```

`invoiced` 只在 `group_by=vendor` 下有意义（拿逐模型成本去对厂商总额发票会得到无意义的结果，所以其他维度下直接返回空）。
差异在 **±2%** 内判为 `reconciled`。超出的判读：

| 判定 | 含义 | 查什么 |
|---|---|---|
| `invoice exceeds the model` | 发票比模型高 | 渠道成本倍率设低了；或有流量没被我们的日志归到这个厂商 |
| `invoice below the model` | 发票比模型低 | 有谈好的折扣还没配进 `ChannelCostRatio` |
| `invoiced but nothing modelled` | 有发票没流量 | 渠道的厂商归属错了 |
| `modelled but not invoiced` | 有流量没发票 | 发票没到，或渠道其实不是这家 |

### 自动化：每天自己跑，不用你想着去查

对账挂在 new-api 自带的 system-task 框架上（`controller/reconcile_task.go`），有数据库租约锁，
多个 master 实例不会重复跑，每次运行留一条 task 记录。

- **跑的是"昨天"，永不跑今天。** 预扣额度是请求时扣、结束后结算，所以进行中的当天收入落后于 token 数，
  看起来像毛利崩了。
- 每次快照三个维度：`model`（什么定错价了）、`vendor`（发票对不对）、`customer`（哪个客户不赚钱）
- 检查间隔 6 小时，但只补还没有快照的日期，所以重启后很快就有数据，也不会重复算
- 关掉：把 option `ReconcileEnabled` 设成 `false`（默认开）

**告警规则**（`service/reconcile_alerts.go`，阈值可调）：

| 级别 | 类型 | 触发 |
|---|---|---|
| critical | `loss-making` | 售价低于建模成本 |
| critical | `unpriced-traffic` | 有流量走了价目表外的模型 → 该行成本偏低、毛利偏高，**不受金额下限限制** |
| critical | `invoice-variance` | 厂商发票**高于**模型 >2% → 有没记账的真实支出 |
| warning | `thin-margin` | 毛利低于 10% → 厂商一涨价就翻负 |
| warning | `invoice-variance` | 发票**低于**模型 >2% → 有谈好的折扣没配进去 |

金额下限 $1：低于一美元的行由取整和单次请求主导，报出来只会淹没真正的问题。
告警按"严重度 → 涉及金额绝对值"排序，所以列表第一条就是该先修的那个。
每条 critical 会**单独**打一行 `RECONCILE critical [...]` 日志——task 记录里的计数容易被划过去，写明哪个模型在亏钱不会。

```bash
# 快照历史（含告警明细）
curl -s "$CONSOLE/api/pricing/reconcile/snapshots?group_by=model&limit=30" \
  -H "Authorization: Bearer $ROOT_TOKEN" | jq '.data[] | {period_start,margin_pct,critical_alerts,alerts}'

# 改完渠道成本/补完价目表后立刻重算某段（force 覆盖已有快照）
curl -s -X POST "$CONSOLE/api/pricing/reconcile/run?start=2026-08-01&end=2026-08-31&force=true" \
  -H "Authorization: Bearer $ROOT_TOKEN"
```

快照保留两年，够做同比；底层日志有自己的清理策略。
存的是**当时算出来的完整报表 JSON**，不做规范化——否则历史快照会随着厂商改价而悄悄变化。

### 三个已知限制

1. **`cached_tokens` 是新加的字段。** 这次改动之前，缓存命中数只存在日志的 `other` JSON 里，SQL 查不到。现在提成了列，但**改动之前的历史行读到 0**——所以那段时间的成本会被高估（缓存读在 Anthropic 只要输入价的 1/10）。方向是保守的：只会低估毛利。
2. **不在 catalog 里的模型算不出成本。** 报表里 `unpriced_requests` / `unpriced_models` 会标出来，那一行的成本是偏低、毛利是偏高的。不会当成 0 成本悄悄算进利润。
3. **20 万行上限。** 聚合粒度是 天×模型×渠道×用户×分组。超了会在响应里带 `warning`、CSV 第一行写 `# WARNING`，**绝不静默截断**。缩小时间范围或加过滤条件重跑。

---

## 四、加一个新模型

模型不在 catalog 里就没有价格，relay 会直接拒绝。这是故意的：定价没定，就不该能卖出去。

1. 在 models.dev 上查到官方价（`https://models.dev/api.json`）
2. 在 `unifyapi_catalog.go` 里加一行：

```go
{Model: "claude-opus-6", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25,
 CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
```

3. 把模型名加进 `unifyapi_baseline_test.go` 的 `publishedModels`（**故意要手动加**——一个读 catalog 来检查 catalog 的测试等于没测）
4. 刷新 fixture：
   ```bash
   go run ./scripts/pricing-drift -save scripts/pricing-drift/testdata/models-dev-<date>.json
   ```
   并更新 `main_test.go` 里的 `fixturePath`
5. `go test ./setting/ratio_setting/ ./scripts/pricing-drift/`
6. 提 PR。合并后交给 CI/CD agent 发布——**不要自己部署**（见 `AGENTS.md`）

models.dev 上查不到官方价的，加 `Unverified: true`，并在行尾注释写清为什么。
漂移检查每次都会把这些列出来，所以不会烂在那里没人管。

---

## 五、厂商改价时

`scripts/pricing-drift` 每周一 06:00 UTC 跑一次（`.github/workflows/pricing-drift.yml`），
发现漂移就开 issue（已有 open issue 就追加评论，不重复开）。

**它不会自动改价。** 改官方价会在部署那一刻改变所有客户的账单，这必须有人拍。

```bash
# 本地手查
go run ./scripts/pricing-drift
go run ./scripts/pricing-drift -json      # 机器可读
```

**厂商涨价时要先问一句**：这个模型现在的毛利吃得下吗？

```bash
curl -s "$CONSOLE/api/pricing/reconcile?start=...&end=...&group_by=model" | \
  jq '.data.lines[] | select(.label=="claude-opus-5")'
```

毛利已经很薄的模型吃不下涨价——那就得同步调 `ModelDiscount`，或者接受毛利下降。

---

## 六、故障速查

| 现象 | 原因 | 处置 |
|---|---|---|
| 客户报 `模型 X 的价格未配置` | X 不在 catalog 里 | 按第四节加进 catalog；不要在后台手填倍率 |
| 启动日志 `PRICING: N model/ratio pairs are billing at...` | 数据库 options 行在覆盖代码基线 | 跑 `seed-pricing.sql`，重启 |
| 折扣改了但价格没变 | 数据库 `ModelRatio` 行在覆盖 | 同上。`/api/pricing/baseline` 的 `shadows` 能确认 |
| 改了补全倍率但不生效 | 命中了 `getHardcodedCompletionModelRatio` 的硬编码锁 | 正常。`claude-3*`、`claude-{sonnet,opus,haiku}-4*`、`o1*`、`o3*`、不带小数点的 `gpt-5*` 的补全倍率被上游锁死。`TestHardcodedCompletionRatioNeverOverridesTheCatalog` 保证锁值和官方价推导值一致 |
| 毛利显示为负 | 售价低于建模成本 | 查 `loss_makers`。要么调折扣，要么该渠道有没配的成本折扣 |
| 对账收入和客户账单不符 | 不应该发生 | 收入直接来自 `logs.quota`。若真不符，是日志写入的 bug，不是报表的 bug |

---

## 相关文件

| 文件 | 作用 |
|---|---|
| `setting/ratio_setting/unifyapi_catalog.go` | 59 个模型的官方报价（唯一真相） |
| `setting/ratio_setting/unifyapi_baseline.go` | 价格 → 倍率的推导、影子检测 |
| `setting/ratio_setting/unifyapi_discount.go` | 模型折扣（客户价） |
| `setting/ratio_setting/unifyapi_channel_cost.go` | 渠道成本（对账） |
| `service/reconcile.go` | 对账引擎（纯函数，可单测） |
| `model/reconcile_query.go` | 日志聚合查询 |
| `controller/pricing_baseline.go` | 基线与折扣接口 |
| `controller/reconcile.go` | 对账接口 + CSV |
| `scripts/pricing-drift/` | 漂移检查器 + 离线 fixture |
| `seed-pricing.sql` | 删掉覆盖代码基线的 options 行 |
| `web/src/features/system-settings/models/baseline-pricing-tab.tsx` | 后台「官方报价与折扣」tab |
| `web/src/features/system-settings/models/channel-cost-tab.tsx` | 后台「上游采购成本」tab |
