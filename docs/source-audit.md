# 源码、依赖与许可证审计基线

审计基线为提交 fbbfa58da1b65fc5d92fae083e06bec1701f8df3。本次文档和容器工作基于同一工作树中的网关、Session、安全通道改动；该基线记录用于说明审计输入，不代表发布或独立安全认证。

## 仓库与安全检查范围

- NotiHarbor（讯港）是基于 JustSong 的 Message Pusher 源码独立衍生的项目。原 MIT LICENSE 与 `Copyright (c) 2022 JustSong` 声明保留在仓库原文中。
- QQ 官方 Bot v2 适配按腾讯官方 HTTP API 与回调签名文档实现；没有复制 qqbot 或其它 GPL-3.0 SDK 的代码。若下游选择 GPL SDK，应单独审查依赖许可证及分发义务。
- API keys 仅以 SHA-256 摘要保存；legacy channel Secret/Token 与敏感系统 option 在启动迁移中用 AES-GCM 加密。
- 自定义 Webhook 对 DNS/IP、重定向、TLS 及私网访问做显式限制；请求/Provider 错误处理见 adapter setup 文档。
- 旧版 authMessage 已修复：任何已配置的 user token 或 channel token 都必须与输入值匹配；比较采用 constant-time。只有两者都未设置时才兼容无 token 的历史开放推送。部署仍应配置 token 或改用 Gateway API key。
- Session 持久密钥、Origin 校验、CSRF、弱默认 root 密码升级及凭据迁移限制见 [安全配置](security.md) 和 [迁移指南](migration.md)。

上游 GitHub issue [#185：IPv6 访问会导致登录失效](https://github.com/songquanpeng/message-pusher/issues/185) 描述过登录后 API 返回 401 的问题。该 issue 是调查线索，不足以单独证明根因；代码审计与证据限制记于 [IPv6/Session 修复说明](ipv6-session-fix.md)。

## Go 与容器构建

Go module 的 go 声明提升到 1.26.0；Gin 固定到 1.12.0，Sonic 固定到 1.15.0，并保留 GORM SQLite/CGO 驱动。依赖解析与 go mod verify 由维护代理完成；未在本地执行 Go build、test 或 vet。CI 才运行测试和完整构建。Docker builder 与 CI 使用 Go 1.26。

Node 20 和 npm lockfile 供 Docker/CI 用 npm ci --ignore-scripts 安装。2026-10-14 npm registry audit 快照返回 102 项 advisories：64 high、35 moderate、3 low、0 critical；受影响直接依赖为 axios、react-router-dom、react-scripts。react-scripts@5.0.1 下游带入大量旧 Jest、Webpack 与 PostCSS transitive 包。npm 给出的自动修复建议涉及主版本升级或不可用的 react-scripts 替代版本。本次没有自动升级 CRA/React Router 栈，避免引入未经验证的重大框架变化；升级应作为独立前端维护工作处理。此快照随 registry 数据变化，不是未来版本的审计结论。

govulncheck 未运行，因该工作流禁止本地构建型检查；因此本页不声称已完成 Go 调用图漏洞扫描，也不对 Go module 给出“无漏洞”结论。仓库 CI 会执行 gofmt、go vet 和模块 checksum 校验；测试与镜像构建只在 GitHub Actions runner 上进行。

## 部署风险记录

- ENCRYPTION_KEY 缺失、格式错误或更换会阻止解密凭据；首次升级前先备份数据库并保管密钥。
- 默认 SQL 日志保留仅清理 90 天以上且已被取代的终态 delivery attempts 与 365 天以上审计日志；更改期限或设为 0 前确认数据库空间和审计政策需求。
- 新版本自动加密既有数据库凭据；回滚旧镜像必须同时恢复升级前数据库。
- SESSION_SECRET 必须稳定，否则已有 Cookie session 失效。
- SQLite 依赖 CGO；容器 build/runtime 使用匹配的 Debian/glibc 环境并启用 CGO。
- QQ Bot v2 实现支持不带 msg_id、event_id 或 is_wakeup 字段的普通主动发送，并支持依赖已验证回调元数据的被动回复。普通主动请求仍可能因机器人权限或目标限制被平台拒绝；相关限额与被动回复窗口以 QQBot 设置文档和腾讯当前官方文档为准。
