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

改写 `model` 是**按字节就地替换**（`sjson.SetBytes`，与本仓库改写请求体的既有写法一致），
不做整包解码再编码。这一点在这里比别处更重要：决策模型的输入就是客户的业务状态，数字是数据
本身；解成 `any` 会把每个数字变成 float64，再编码回去就变了样——id `9007199254740993` 变成
`...992`，`12345678901234567890` 丢掉末尾三位。客户会为一次针对他们从没发过的数字做出的判断
付费，而且全链路不会有任何提示。就地替换连字段顺序和我们没见过的键一起保住，上游读到的就是
客户写的那份，只有模型名不同。嵌套在 `state` 里名为 `model` 的字段是客户的数据，不会被动。

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
| FlatKey | `https://router.flatkey.ai` | `/api/alpha/decisions` | `jev-1.13` → `typesafe/jev-1.13` | 厂商给出 + 端点探测（401，而同前缀的对照路径 301） |

### OpenRouter 这条核实到什么程度（别把话说满）

已证实的：`https://openrouter.ai/api/v1/systemone` 返回 401（有鉴权网关挡着的真实路由），
同一主机上乱填的路径返回 404 `Not Found`，且**没有重定向**。请求体与厂商一致
（`{model, state, questions}`）。

**尚未证实的**：`typesafe/jev-1.13` 不在 OpenRouter 公开的 `/api/v1/models` 里（该列表 445 个
模型，无一条 typesafe/jev）。那个列表是 chat 模型目录，System One 是另一个接口面，不在里面是
合理的，但**这只是推断，不是证据**。另外注意 `https://openrouter.ai/typesafe/jev-1.13` 这种
模型页对**乱填的模型名也返回 200**（前端 SPA 兜底），所以"页面能打开"不能作为证据。

要坐实只有一个办法：拿一把 OpenRouter 的 key 发一次真实请求。在那之前，这个上游是"路由确认
存在、模型 id 未确认"。

### FlatKey 的端点不在 `/v1` 下（2026-09-22 更正）

FlatKey 把 System One 挂在 **`/api/alpha/decisions`**，不在 `/v1` 命名空间里，模型名用
`typesafe/jev-1.13`。请求体与厂商一致（`questions` 是以问题名为键的**对象**，不是数组），我们的
`SystemOneRequest.Questions` 正是 `map[string]any`，直接对得上。

端点探测（401 表示真实路由被鉴权挡住，同前缀的乱填路径返回 301 兜底跳转）：

```
POST https://router.flatkey.ai/api/alpha/decisions   -> 401  Token not provided
POST https://router.flatkey.ai/api/alpha/zzz-control -> 301  （对照组）
POST https://router.flatkey.ai/api/zzz-control       -> 301  （对照组）
```

**之前为什么会 404。** 端点路径留空时我们发往 `/v1/systemone`；`router.flatkey.ai` 把**所有**
未知路径 301 跳到 `console.flatkey.ai`，Go 的客户端默默跟随，跨域时按 net/http 的规则丢掉
`Authorization`，于是 console 用它自己的 404 回答，报错里只剩一个路径名，看不出答话的是另一台
主机。现在这种情况会在错误信息里点名最终 URL。

**这里踩过一个推理上的坑，值得记住。** 我扫了 `/v1/systemone`、`/v1/system-one`、
`/v1/typesafe/systemone`、`/typesafe/v1/systemone`、`/api/v1/systemone`，每条都配了对照组，
全部不存在，于是得出"FlatKey 没有 System One 端点"。**这个结论不成立**：对照组只能证明
*我试过的那些路径*不存在，永远证明不了端点不存在——穷举猜测无法证否。真实路径是
`/api/alpha/decisions`，"decisions"这个词根本不在我的猜测集合里。**路径要向上游要，不要靠猜；
穷举猜不中只说明猜错了，不说明东西不在。**

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
