# QQ 官方 Bot v2 通道

此通道使用 QQ 官方 Bot API v2 发送 C2C 私聊或群聊文本/Markdown，支持普通主动消息、被动回复和 C2C 互动召回。HTTP 回调用于安全采集 OpenID 和被动回复所需的事件；主动消息本身不依赖回调。实现独立依据腾讯公开协议编写，没有复制第三方 SDK 源码。

官方文档在此处存在时间差：当前“消息收发概述”将普通主动消息列为“无条件”消息，并说明用户可在客户端关闭“允许主动发送”；旧版总发送页仍保留“主动推送自 2025-04-21 停止”的警告。实现以当前总览和当前 API v2 端点字段为准；平台实际权限仍以每个机器人、目标关系及 QQ 客户端设置为准。

## 发送模式

`delivery_mode` 接受以下三种值：

| 模式 | 请求字段 | 支持范围 |
| --- | --- | --- |
| `active` | 不发送 `event_id`、`msg_id`、`msg_seq` 或 `is_wakeup` | 私聊和群聊；目标必须已建立关系且机器人拥有主动消息权限 |
| `passive` | 发送可用的 `event_id` 或 `msg_id`；有 `msg_id` 时使用 `msg_seq` | 回复已验证的入站事件，受平台回复窗口和次数限制 |
| `wakeup` | 仅发送 `is_wakeup: true`，不发送被动关联字段 | 仅 C2C 用户；用户先前与机器人互动后，每个官方周期最多一条 |

配置省略 `delivery_mode` 时默认为 `active`。用户可以在 QQ 客户端关闭“允许主动发送”，该设置会使主动消息失败；未取得主动消息权限也会由平台拒绝。适配器不会用失败的被动消息自动重发为主动消息，也不会在普通主动和 wakeup 之间自动切换。

官方总览（2026-07-21 更新）列出 C2C 被动回复 60 分钟/最多 4 次，群聊 5 分钟/最多 5 次；当前 C2C API 端点的 `msg_id` 字段说明仍写 5 分钟。为避免窗口不一致导致额外拒绝，本适配器对 C2C `msg_id` 和 `event_id` 回复均采用 5 分钟上限，并将 C2C 序号限制为 4、群聊限制为 5。平台仍是最终校验方。C2C wakeup 的官方周期为当天、1–3 天、3–7 天和 7–30 天；具体资格由 QQ 服务端判断。

API v2 单聊目标使用用户 OpenID，群聊目标使用群 OpenID。不能用 QQ 号或普通群号代替 OpenID。文本使用 `msg_type: 0`，Markdown 使用 `msg_type: 2`。建议将 API 请求超时设为至少 5 秒，以降低平台已接收但客户端未收到响应的情况。

## 配置

在通道管理中选择 `QQ 官方 Bot v2`，填写：

| 字段 | 内容 |
| --- | --- |
| AppID (`app_id`) | QQ 开放平台机器人 AppID |
| Client Secret (`secret`) | QQ 开放平台 Client Secret；只提交，不回显 |
| 默认目标 (`other.default_target`) | 可选的用户或群 OpenID；旧配置可继续使用 `account_id` |
| API 地址 (`url`) | 留空使用当前生产 API `api.bot.qq.com`；联调可选官方沙箱。旧 `api.sgroup.qq.com` 仅可显式设置 |
| `other` | 下方 JSON 配置 |

`secret` 存入通道加密凭证字段。不要把它写进 `other`、`url`、消息内容或自定义字段。访问令牌由适配器申请并按 AppID/Secret 缓存，不要求手工填写或回显。

示例 `other`：

```json
{
  "bot_name": "通知机器人",
  "default_target": "由官方事件采集的 OpenID",
  "target_type": "user",
  "delivery_mode": "active",
  "message_type": "markdown",
  "markdown_template_id": "",
  "bot_certified": false,
  "timeout_seconds": 10,
  "rate_limit_per_second": 5,
  "bot_rate_limit_per_minute": 30,
  "target_rate_limit_per_minute": 20,
  "target_rate_limit_per_day": 1000,
  "retry_max_attempts": 5,
  "retry_backoff_seconds": [30, 120, 300, 900]
}
```

`default_target` 可设为用户或群 OpenID，并由 `target_type` 指定类型；解析优先级为消息/已验证事件的目标、`other.default_target`、旧配置 `account_id`。`delivery_mode` 可设为 `active`、`passive` 或 `wakeup`；`message_type` 可设为 `text` 或 `markdown`。如果使用自定义 Markdown 模板，请在开放平台配置模板并填写 `markdown_template_id`。队列读取 `retry_max_attempts` 和 `retry_backoff_seconds`；它们不会延长平台回复窗口或每日额度。

未认证配置默认共享限流为机器人 5 次/秒、30 次/分钟；将 `bot_certified` 设为 `true` 后，C2C 默认上限为 10 次/秒、600 次/分钟，群聊为 60 次/分钟。所有配置还受单目标 20 次/分钟和 1000 次/天限制。发送限流通过 Redis 原子脚本在 worker 间共享；未启用或不可用时，发送会在调用 QQ 前失败并返回可重试的限流后端错误。`bot_certified` 是管理员依据开放平台控制台认证状态显式设置的限流选项，适配器不声称通过不存在的权限查询 API 自动探测认证状态。

## 回调与 OpenID 采集

API 路由层应把 `channel.NewQQBotWebhookHandler(resolve, onEvent)` 注册为 `POST /api/v1/qqbot/callback`，并将公网 HTTPS 回调地址填入 QQ 开放平台。代理需要保留 `X-Bot-Appid`、`X-Signature-Timestamp` 和 `X-Signature-Ed25519`，并原样转发请求体。handler 按 `X-Bot-Appid` 查询唯一启用的 `qqbot_v2` 通道；未知 AppID 返回 403，存储故障返回 503。

普通事件先用通道 Client Secret 派生的 Ed25519 公钥验证时间戳和原始请求体，再解析事件并交给应用事件 sink。回调验证挑战（`op: 13`）由 handler 按相同密钥规则生成响应；有效 webhook 回调会收到 `op: 12` ACK。应用 sink 需要按通道和事件/消息 ID 原子去重、记录事件元数据，并 upsert 有效的用户或群 OpenID。群事件同时可能包含群目标和发言者 OpenID：群回复使用群 OpenID；C2C 回复使用用户 OpenID。相同事件重放时应返回成功而不重复入队；持久化失败返回 503。

事件外层的 `s` 是网关事件序号，记录为 `QQBotCallbackEvent.MessageSequence`，不等于发送 API 的 `msg_seq`。队列为同一 `msg_id` 管理回复序号：C2C 从 1 开始且不超过 4，群聊不超过 5；只有可用 `event_id` 时使用事件 ID 被动回复。普通主动发送与 C2C wakeup 不使用这些被动关联字段。

## 失败与重试

HTTP 429、40034100 主动限频、可重试的 5xx 与共享限流服务故障会带分类、状态码和可能的 `Retry-After`。401 会在一次发送调用内并发安全地刷新令牌后重试一次。主动消息无权限（40034105）、用户拒收（40054013）、召回额度用尽（40034122）、不支持召回（40034123）和被动窗口/次数超限（40034128）均作为不可重试错误分类。平台返回的 304023/304024 审核受理码记为已接受；成功 HTTP 状态若响应体无法解析或缺少平台消息 ID，则标记为未知结果。发送后超时或连接断开也可能已经投递，会标记为 `DeliveryUnknown()` 且 `SendAttempted()`，队列应避免盲目自动重发；token 获取、Redis 预检和本地配置错误标记为尚未尝试发送。

## 官方资料与核验记录

按 2026-10-14（Asia/Singapore）核验的官方资料：

- [消息收发概述](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html)（页面标注更新于 2026-07-21）— 当前主动消息、被动回复、C2C wakeup、认证状态的速率规则与目标关系额度。
- [群聊消息事件：GROUP_MESSAGE_CREATE](https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_message_create.html)（页面标注更新于 2026-09-16）— 开启接收所有群消息后收到该事件；事件 `d.id` 可用于被动回复。
- [API 调用说明](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/api-call-guide.html)（页面标注更新于 2026-09-17）— 当前统一 API host `api.bot.qq.com` 及 AppID 维度的 OpenID 说明。旧 `api.sgroup.qq.com` 在适配器中只为显式兼容配置保留。
- [C2C 发送接口](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_users_user_openid_messages.post.html)（页面标注更新于 2026-09-03）— `POST /v2/users/{openid}/messages`；`is_wakeup` 与 `msg_id`/`event_id` 互斥；列出 40034100 主动频控、40034105 无主动权限、40054013 拒收、40034122 召回周期用尽、40034123 不支持召回和 40034128 被动窗口/次数限制等错误码。
- [群聊发送接口](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html) — `/v2/groups/{group_openid}/messages`，支持普通主动及被动字段；没有 C2C 的 `is_wakeup`。
- [旧版总发送页](https://github.com/tencent-connect/bot-docs/blob/main/docs/develop/api-v2/server-inter/message/send-receive/send.md) — 仍保留 2025-04-21 主动消息停用警告，且 C2C 被动回复段与当前概述/端点行有窗口差异。本实现记录此冲突并保守采用较短 C2C `msg_id` 回复窗口。
- [获取访问凭证](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/access-token.html) — 当前 token endpoint、`appId`/`clientSecret`、`expires_in`、刷新规则、Authorization 格式及业务错误码。
- [事件订阅与通知](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/event-emit.html) 和 [签名校验算法](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/sign.html) — callback payload、ACK、签名种子、挑战响应和原始请求体验签规则。目标采集识别 `C2C_MESSAGE_CREATE`、`GROUP_AT_MESSAGE_CREATE`、`GROUP_MESSAGE_CREATE`、`INTERACTION_CREATE` 及官方关系/偏好事件；仅消息事件 `d.id` 作为 `msg_id`。
- [腾讯官方 Node.js SDK 使用指南](https://github.com/tencent-connect/qqbot-nodejs/blob/main/USAGE.md) — `msgId` 是否存在区分主动发送与被动回复；仅作协议交叉检查，未复制 SDK 源码。

本适配器不启用富媒体、Ark、Embed、键盘、频道/频道私信或 WebSocket Gateway。
