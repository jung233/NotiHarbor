# NotiHarbor 安全配置和运行边界

部署前请阅读 [部署指南](deployment.md) 与 [迁移指南](migration.md)，特别是固定加密密钥、数据库备份和回滚要求。

## 身份、Session 与密钥

- 新安装通过 INITIAL_ADMIN_USERNAME/INITIAL_ADMIN_PASSWORD 创建管理员。初始密码至少 12 个字符；历史 root 若使用 123456，启动会要求该变量并轮换密码。
- 注册默认关闭。若管理员之后在系统设置中开启注册，应在公开服务前重新评估账户访问策略。
- SESSION_SECRET 至少 32 字节，且多实例必须使用同一个固定值。HTTPS 的 APP_PUBLIC_URL 会强制 Secure Cookie；Cookie 为 HttpOnly、host-only，SameSite 默认 Lax。
- 对浏览器带状态请求，Session 中间件校验 Origin 或 Referer 的 scheme/host（含端口）。部署代理时只信任 TRUSTED_PROXIES 中明确列出的代理。
- API key 使用 Bearer Header；key 仅存 SHA-256 摘要，创建/轮换明文只返回一次。每个 source 的 keys 共用一个速率计数器与额度。
- 通道 Secret/Token 与系统选项里的敏感 Secret/Token 使用 ENCRYPTION_KEY 做 AES-GCM 加密。备份此密钥，保持长期不变；更换密钥前需另行设计完整重加密流程。

## 网络和存储

- 使用 HTTPS 反向代理。Compose 只绑定 127.0.0.1，不要在防火墙或云安全组中直接暴露容器端口。
- SQL DSN 和 Redis URL 内含凭据，应存入 secret store 或限制 .env 权限。不得把 bearer key、渠道 token、SMTP password 或凭证放入 URL、查询参数、日志或 issue。
- SESSION_STORE=cookie 可用于带 TLS 或 ACL 用户名的 Redis，同时保留 Redis Streams 作为网关队列；Redis Session 本身支持所选逻辑 DB。Redis 不可用时网关不能发送；/readyz 返回 503。
- 自定义 webhook 默认仅允许公网 HTTPS 目标，解析、重定向和敏感头受限制；启用 allow_private_network 前确认目标属于受控内网。详见 [Webhook 指南](webhook-setup.md)。
- 运行容器使用非 root UID 10001，只需对持久化 /data 有写权限。
- 日志保留默认清理过期的 superseded terminal delivery attempts（90 天）和审计记录（365 天）；每个 delivery 的 latest attempt、unknown/dead_letter 全量尝试、通知/幂等状态、delivery、outbox 与 QQ 回调去重记录会保留。应用文件日志应由部署环境轮转。

## 日志与兼容接口

请求日志包括 request/trace ID，不记录 query、header 或 body。Provider 响应正文不会整体保存，错误记录保留分类与短错误码。操作系统和反向代理仍应避免把敏感 URL 写进访问日志。

旧版 /push/:username 支持旧 token 参数。若用户 token 或通道 token 已配置，请求必须提供匹配其一的 token；只有两者都未配置时才保持旧式开放推送行为。该兼容接口的安全边界较宽，新集成应使用有 scope 的 Gateway API key。
