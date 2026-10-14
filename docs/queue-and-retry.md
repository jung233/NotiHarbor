# NotiHarbor 队列、状态与重试

网关用 SQL 保存权威状态，并用 SQL outbox 与 Redis Stream 将新 delivery 分发给 worker。创建 notification、deliveries 与 outbox 事件发生在一个数据库事务内。因此 Redis 短暂中断不会让已提交的任务消失；publisher 恢复后会继续发布。

worker 在 SQL 中领取 delivery 租约、创建 attempt，再调用对应通道。租约会续期；worker 在发送期间丢失且租约过期时，服务把结果记为 delivery_unknown，因为 provider 可能已经接受请求。处理完毕后，worker 更新 SQL 状态，再确认并删除 Redis Stream 条目。Redis Stream 不是状态数据库。

兼容的旧版 /push/:username?async=true 在 Redis 网关队列启用时，会把 Message 与网关 notification、delivery、outbox 一起写入 SQL 事务，同时保留旧版 HTTP 200 和 uuid 响应。桥接记录会从旧消息扫描器中排除，防止同一消息由两套 worker 重复发送。

## 状态

Notification 可能为 pending_dispatch、queued、processing、retrying、accepted、failed、cancelled 或 delivery_unknown。单个 Delivery 还可能是 held、dead_letter。

- accepted 表示 provider 接受了请求，不代表用户已阅读。
- failed 表示终止失败或发送前检查失败。
- retrying 表示安排了下一次发送。
- delivery_unknown 表示请求可能已发送但无法确认。
- dead_letter 表示可重试错误已经耗尽该 delivery 的发送预算。它是 SQL delivery 状态，不是独立的 Redis 死信队列。
- held 用于尚未启用的 fallback 路由。

每个 delivery 默认最多发送 5 次，默认延迟依次为 30 秒、2 分钟、5 分钟、15 分钟。Retry-After 优先于默认延迟。当前默认没有随机抖动。

Outbox 扫描优先级依次为 critical/urgent、high、normal、low；同优先级按待处理时间调度。若已保存的重试退避项少于后续所需次数，最后一个退避值重复使用。

## 幂等和手动操作

幂等范围为 source 与 idempotency key。相同 key 且请求内容一致会返回原 notification；不同内容冲突返回 HTTP 409。HTTP 202 表示通知已持久化，后续仍需查询投递状态。

worker 收到可能已由 provider 接受的超时或连接中断后，会默认标记为 delivery_unknown 并停止自动发送，降低重复通知风险。管理员或持有 notifications:retry scope 的客户端可显式重试；重复发送风险由调用方承担。resend 会创建新的 notification，而不是延续原 notification 的幂等身份。

fallback 按 route order 顺序执行。只有已知的 failed 或 dead_letter 会启用下一路；delivery_unknown 不会自动切换，以免前一个渠道已投递时产生重复通知。broadcast 会对所有选中的通道分别投递。

重试可用 Retry-After 和 next_retry_at 查看计划，使用 attempts 路由查看每次发送和分类后的错误码。响应不会保存 provider 的完整错误正文。

## 日志保留

Redis gateway worker 启用时，后台每小时最多分别清理 500 条过期投递尝试和 500 条审计记录。`DELIVERY_LOG_RETENTION_DAYS` 默认为 90 天，只删除状态为 accepted、cancelled 或 failed 的 delivery 上较旧且已被后续 attempt 取代的记录；每个 delivery 的最新 attempt 始终保留。`delivery_unknown` 和 `dead_letter` delivery 的全部 attempts 不清理。`AUDIT_LOG_RETENTION_DAYS` 默认 365 天，超过期限的审计记录删除。任一设置为 0 可关闭对应清理，允许范围是 0–36500 天。

通知与幂等记录、delivery 状态、outbox 事件、QQ 回调去重事件和消息序号不会由此任务清理。应用文件日志的轮转由部署环境负责；本任务不提供日志轮转器。
