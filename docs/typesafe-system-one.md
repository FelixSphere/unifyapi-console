# TypeSafe System One 渠道

TypeSafe 的 System One 模型（目前是 Jev）不生成文本。客户传一个 **state** 和一组命名的
**typed questions**，模型返回每个问题的类型化答案和校准后的概率。没有自回归解码，因此
没有 assistant 消息、没有流式输出、也没有可计费的输出 token。

这就是它需要独立渠道类型和独立端点的原因：它的价值全在那个结构里，把概率表压成一条
assistant 消息正好丢掉唯一有用的东西。

## 客户怎么调用

把 TypeSafe SDK 或 curl 指向我们的 base URL，用我们签发的 key：

```bash
curl https://app.unifyapi.ai/v1/systemone \
  -H "Authorization: Bearer $UNIFYAPI_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-1.13",
    "state": "客户说这张发票金额不对，要求退款",
    "questions": {
      "is_billing": {
        "type": "noul",
        "instructions": "这是账务问题吗？",
        "criteria": {"true": "和钱有关", "false": "其它"}
      },
      "urgency": {
        "type": "score",
        "instructions": "紧急程度",
        "criteria": ["不急", "一般", "很急"]
      }
    }
  }'
```

请求体和响应体**原样透传**。我们只在渠道配置了模型重命名时改写 `model` 字段，其余
字段一个不动——包括 TypeSafe 以后新增的字段。

问题类型有三种：`noul`（是/否）、`choice`（多选一）、`score`（按档位打分）。完整语义见
厂商文档 <https://docs.typesafe.ai/api>。

## 这个渠道不支持什么

对该渠道发起 `/v1/chat/completions`、`/v1/embeddings`、`/v1/rerank`、音频或图片请求会被
**明确拒绝**，错误信息会指向 `/v1/systemone`。这是有意的：chat 请求里没有 state，也没有
typed questions，任何映射都只能是我们发明的，客户却要为这个猜测付费。

## 运营怎么配

**渠道 → 创建渠道**，类型选 **TypeSafe (System One)**，然后按上游填：

| 上游 | Base URL | System One 端点路径 | 模型映射 | 核实情况 |
|---|---|---|---|---|
| TypeSafe 直连 | 留空（即 `https://api.typesafe.ai`） | 留空 | `jev-1.13` → `jev-1.13.0` | 厂商文档 |
| OpenRouter | `https://openrouter.ai/api` | 留空 | **不需要**，裸名 `jev-1.13` 会自动映射到 `typesafe/` 命名空间 | 文档 + 端点探测（`/api/v1/systemone` 返回 401，乱填的路径返回 404） |
| FlatKey | **用不了**，见下 | — | — | 2026-09-21 实测：其网关没有任何 System One 端点 |

### FlatKey 为什么配不通（2026-09-21 实测，不需要密钥即可复现）

FlatKey 转售 `typesafe/jev-1.13`，但**只通过 OpenAI 兼容的 chat 接口**，没有开放厂商原生的
System One 端点。我们这个渠道故意不接受 chat（没有 state 和带类型的问题，任何映射都是编的），
所以 FlatKey 目前无法作为 Jev 的上游，换哪个路径都不行。

证据（每条都配了一个"乱填路径"的对照组，单看 401 或 404 都会得出错误结论）：

```
POST https://router.flatkey.ai/v1/systemone          -> 301  Location: https://console.flatkey.ai/v1/systemone
POST https://router.flatkey.ai/v1/zzz-does-not-exist -> 301  （对照组：完全相同，说明这是兜底跳转，不是真路由）
POST https://console.flatkey.ai/v1/chat/completions  -> 401  {"message":"Token not provided","type":"new_api_error"}
POST https://console.flatkey.ai/v1/systemone         -> 404  {"message":"Invalid URL (POST /v1/systemone)"}
POST https://console.flatkey.ai/v1/zzz-does-not-exist-> 404  （对照组：与上一条字字相同）
```

`/v1/chat/completions` 返回 401 而 System One 的所有候选路径都和对照组一样返回 404，
这才说明前者是真实路由、后者根本不存在。同时试过 `/v1/system-one`、`/v1/typesafe/systemone`、
`/typesafe/v1/systemone`、`/api/v1/systemone`，无一例外。

运营看到的报错是这样来的：`router.flatkey.ai` 把**所有**未知路径 301 跳到
`console.flatkey.ai`，Go 的 HTTP 客户端默默跟随，跨域时按 net/http 的规则丢掉
Authorization 头，于是 console 以 404 回答，报错里只剩一个路径名，看不出答话的其实是另一台主机。
现在这种情况会在错误信息里点名最终 URL。

"System One 端点路径"是渠道设置里的一个可选字段：留空走厂商的 `/v1/systemone`；聚合商把这套
API 挂在别处时填它们的路径即可，不需要改代码。Base URL 已经以该路径结尾时不会重复拼接。

密钥填对应上游的 API key。模型列表默认预填 `jev-1.13`。

**别名不要卖。** 厂商的 `jev-latest` / `jev-preview` 会随厂商换代改变客户实际买到的模型和价格，
目录里只有带版本号的 `jev-1.13`。

厂商自己的版本化 id 是 `jev-1.13.0`，另有别名 `jev-latest` / `jev-preview`。我们对外卖的名字
是 `jev-1.13`，所以**直连 TypeSafe 的渠道需要加一条模型映射** `jev-1.13` → `jev-1.13.0`。
别名没有被列入销售目录：别名会在厂商换代时悄悄改变客户买到的是哪个模型、以及按哪个价格
计费。

## 计费

按 `usage.input_tokens` 计费，输出免费（目录里 `jev-1.13` 的 `OutputUSD` 为 0 并带
`FreeOutput` 标记，原因见 [定价与折扣](PRICING-AND-DISCOUNTS.md)）。

两条值得知道的保护：

- **响应没有 `usage` 就拒绝**，不按零计费。一次无法对账的调用要么记成免费漏收，要么被
  我们凭空估一个数字，两者都不可接受。
- **单次请求的问题数量有上限**（`dto.MaxSystemOneQuestions`）。整个 state 会按每个问题
  重读一遍并计入输入 token，所以不设上限等于不设账单上限。

## 预扣费的估算依据

预扣费按 state 加上每个问题的 instructions 和 criteria 的文本量估算，结算时以厂商返回的
`usage` 为准。state 是对象或数组时会递归展开计算，不会因为它不是字符串就估成 0。

## 渠道测试

后台的"测试渠道"对 TypeSafe 渠道会自动发一个最小的 System One 请求（一个 `noul` 问题、
state 为 `ping`），而不是聊天请求——端点类型选"自动检测"即可，选错也没关系，渠道类型说了算。
下拉里也有一项 `TypeSafe System One (/v1/systemone)`，手动选它是同样的效果。

早期版本有两个先后暴露的问题，看到对应错误说明跑的是修复前的版本：

- `typesafe serves System One requests at /v1/systemone` —— 测试按聊天请求发出，被适配器
  按设计拒绝（v0.1.107 修复）。
- `invalid relay format` —— `GenRelayInfo` 里没有这个格式的分支，**任何**经过鉴权的
  `/v1/systemone` 请求都会在这一步失败，不只是渠道测试。未鉴权探测返回 401 是鉴权中间件
  挡下的，走不到这一层，所以当时没暴露出来。现在有一个守卫测试扫描路由表，要求每个对外
  分发的 relay format 都必须有对应分支。
