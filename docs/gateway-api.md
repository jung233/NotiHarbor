# NotiHarbor 统一 REST API

机器对机器接口前缀为 /api/v1。完整的可机读路径见 [OpenAPI 3.1 描述](openapi.yaml)。旧版 /push/:username 和 /api/... 页面接口继续保留。

## 认证与响应

上游 API 仅接受 Authorization: Bearer <api_key>。key 属于一个 source，权限按 scope 控制；认证信息不从 Cookie、查询参数或表单读取。管理接口使用已登录的浏览器 Session Cookie，并执行 Origin/Referer 校验。创建和轮换 key 的明文只出现在该次响应的 data.api_key 中。

成功响应为 { "success": true, "message": "", "data": ... }；失败响应为 { "success": false, "message": "..." }。分页结果的 data 为 { "items": [], "total": 0, "page": 1, "page_size": 20 }。默认每页 20 项，最大 100 项。请求 JSON 限制为 1 MiB，拒绝未知字段和多个 JSON 值。

每个 source 的 key 共用每分钟计数器。默认上限 60 次/分钟，可由 source 的 rate_limit_per_minute 修改；超限返回 429 和 Retry-After。Redis 计数器不可用时请求返回 503。

## Upstream API

| 方法 | 路径 | Scope | 行为 |
| --- | --- | --- | --- |
| POST | /api/v1/notifications | notifications:write | 创建通知；首次接收返回 202，幂等重放返回 200 |
| GET | /api/v1/notifications | notifications:read | 分页查询本 source 的通知 |
| GET | /api/v1/notifications/{id} | notifications:read | 返回通知及其 deliveries |
| GET | /api/v1/notifications/{id}/deliveries | notifications:read | 列出投递 |
| GET | /api/v1/notifications/{id}/deliveries/{deliveryID}/attempts | notifications:read | 分页列出尝试 |
| POST | /api/v1/notifications/{id}/retry | notifications:retry | 手动重试 |
| POST | /api/v1/notifications/batch/retry | notifications:retry | 对 1–100 个通知请求重试 |
| POST | /api/v1/notifications/{id}/cancel | notifications:cancel | 取消可取消的投递 |
| POST | /api/v1/notifications/{id}/resend | notifications:write | 创建新的通知副本 |
| GET | /api/v1/channels | channels:read | 列出 source 获准使用的启用通道 |
| POST | /api/v1/channels/{channelID}/test | channels:test | 发送并审计一条测试消息 |
| GET | /api/v1/logs | logs:read | 分页读取本 source 投递日志 |

创建通知示例：

```http
POST /api/v1/notifications
Authorization: Bearer <api_key>
Idempotency-Key: order-2026-00042
Content-Type: application/json

{
  "title": "订单已完成",
  "content": "订单 42 已完成",
  "priority": "normal",
  "message_type": "text",
  "channels": ["12"],
  "targets": {"12": "target-uuid"},
  "delivery_policy": "broadcast",
  "metadata": {"order_id": "42"}
}
```

title 和 content 必填，channels 需要包含 1–16 个本 source 被授权的通道 ID 或通道名。message_type 支持 text 和 markdown，delivery_policy 支持 broadcast 和 fallback。可用 JSON 字段 idempotency_key 或 Idempotency-Key 请求头指定幂等键；未提供时服务器生成并在响应中返回。幂等键最长 191 字节。键相同但请求内容不同会返回 409。

查询支持 page、page_size、RFC3339 的 from/to、status、channel_id、error 和 search。GET 列表也可通过 source_id 筛选；API key 只能访问它自己的 source。对 Session 管理端点，非 root 用户查询 logs 时必须提供 source_id；API key 查询 logs 时 source_id 可省略，若提供则必须与 key 所属 source 相同。

测试通道请求可包含 target_id、title、content、message_type 和 is_test。API key 调用时 source_id 可省略，并会绑定到 key 对应的 source；如传入，必须匹配。成功的测试调用返回 data.attempt、data.is_test=true 和 data.delivery_id，并写入审计记录。

## Session 管理接口

以下路由均要求登录 Session。普通管理员只能管理自己拥有的 source 和通道；root 管理员可以跨用户查看。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | /api/v1/admin/dashboard | 通知总览 |
| GET | /api/v1/admin/notifications 及其详情、deliveries、attempts 路由 | 浏览和诊断通知 |
| POST | /api/v1/admin/notifications/{id}/retry、cancel、resend | 管理通知 |
| POST | /api/v1/admin/notifications/batch/retry | 批量重试 |
| GET | /api/v1/admin/channels | 查看当前用户启用通道 |
| POST | /api/v1/admin/channels/{channelID}/test | Session 方式发送测试消息 |
| GET | /api/v1/admin/logs、/api/v1/admin/audit-logs | 查看投递与管理审计记录 |
| GET/POST | /api/v1/admin/sources | 列表或创建 source |
| GET/PATCH/DELETE | /api/v1/admin/sources/{sourceID} | 管理 source |
| GET/POST | /api/v1/admin/sources/{sourceID}/api-keys | 列表或创建 API key |
| POST | /api/v1/admin/sources/{sourceID}/api-keys/{keyID}/rotate | 轮换 key |
| DELETE | /api/v1/admin/sources/{sourceID}/api-keys/{keyID} | 撤销 key |
| GET/POST | /api/v1/admin/sources/{sourceID}/targets | 列表或新增目标 |
| PATCH/DELETE | /api/v1/admin/sources/{sourceID}/targets/{targetID} | 更新或删除目标 |

创建 key 时未传 scopes，默认授予 notifications:write 和 notifications:read。可用 scope 为 notifications:write、notifications:read、notifications:retry、notifications:cancel、channels:read、channels:test、logs:read。

## 运维端点与回调

- GET /healthz 仅报告进程存活。
- GET /readyz 检查 gateway、数据库和 Redis；未就绪时返回 503。
- POST /api/v1/qqbot/callback 是 QQ 官方 Bot v2 webhook，不使用 API key；它以 X-Bot-Appid 定位启用通道并验证官方签名。详情见 [QQBot 指南](qqbot-setup.md)。

旧版 /push/:username?async=true 仍返回 HTTP 200 与 uuid；Redis 网关队列启用时，它会在同一事务内写入旧消息及网关投递记录，并由旧扫描器跳过已桥接的消息，以免重复发送。旧状态查询路径继续可用。
