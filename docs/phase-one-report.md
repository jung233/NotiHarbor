# 第一阶段交付报告

## A. 开发摘要

在原 Go/Gin、React 项目上完成源码改造，保留 MIT 许可证及原项目来源。五个 `gpt-6-luna / max` 子代理分别承担队列、API、适配器、前端、部署文档，主代理统筹认证、凭证迁移、旧接口桥接和静态集成审查。

新增统一通知 API、来源与 API Key 权限、目标管理、Outbox/Redis Streams、发送记录、重试/死信/回退、QQ 官方 v2、SMTP、Webhook、Dashboard 和管理页面。原 `/push/{username}` 异步请求接入事务队列，保留旧接口响应。普通主动推送已编写实现，不再按旧文档禁止。

这是源码与静态检查交付，尚不是运行验收通过的版本。

## B. IPv6 登录 Bug

发现原代码启动时随机生成 SessionSecret、忽略 Redis Session 初始化错误、未明确可信代理和 Cookie 安全属性、直接断言异常 Session 字段。现强制稳定至少 32 字节密钥，处理 Store 初始化错误，使用 Host-only/HttpOnly/SameSite Cookie、HTTPS Secure、明确可信代理、重复 Cookie 拒绝、类型安全会话和同源修改保护。前端启动重新查询身份并处理 401。

不能从源码证明 IPv6 冒号是非法 Base64 的原因；原代码并未将 Session 绑定客户端 IP。Cookie 截断、代理改写、重复 Cookie、实例密钥不一致仍需部署证据。已编写 IPv4/IPv6 地址切换、重启、坏 Cookie、密钥变化、同源保护及 CI Redis 会话测试，均未执行。原始 Issue #185 尚待真实双栈网络验证。详见 `docs/ipv6-session-fix.md`。

## C. QQBot 功能

纠正先前依据旧 GitHub 文档得出的“普通主动停用”结论。当前[官方消息概述](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html)支持普通主动消息，用户关闭允许主动发送时会失败。[当前 API 调用指南](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/api-call-guide.html)使用 `https://api.bot.qq.com`。

已实现 C2C/group 普通 active：请求不携带 `msg_id`、`event_id`、`msg_seq`、`is_wakeup`。被动回复验证持久化回调，并分配 AppID/消息/目标类型隔离的序号；wakeup 是独立 C2C 模式，校验最近 30 天真实互动。普通主动不要求近期回调。

Token 并发缓存、临近到期刷新、401 后一次刷新重试；OpenID 按机器人隔离，可手动保存或从验签回调发现。支持文本/Markdown，Redis 共享 Bot/目标/日限额，默认采用保守未认证额度。错误区分频控、权限、拒收、过期、召回额度、网络及未知结果；2xx 仍检查业务响应，损坏响应不会误报成功。官方 C2C 概述为 60 分钟/4 次，字段文档仍写 5 分钟，本系统明确采用 5 分钟/4 次保守窗口；群为 5 分钟/5 次。

签名、回调、payload、Token、错误分类测试已编写，真实 QQBot 发送未执行。详见 `docs/qqbot-setup.md`。

## D. 可靠队列

通知、Delivery、Outbox 同 SQL 事务保存；Redis Streams 为调度入口而非唯一消息存储。Worker 使用数据库租约、续租、PEL 认领、持久化尝试日志和状态转换。通知锁与投递锁顺序统一，状态汇总串行；有效租约的 PEL 不提前 ACK。各条投递独立保存 QQ metadata，防止多通道覆盖。

支持来源内幂等键/内容冲突、自动退避、Retry-After、手动重试/重发、批量重试、死信、明确失败回退。默认不自动重试未知结果；外部平台无法保证严格 exactly-once。Outbox 按消息优先级选择，但已进入 Streams 的消息不保证全局优先级顺序。

日志默认保留旧尝试 90 天、审计 365 天，可用 0 禁用；每小时分批清理。始终保留最新尝试、未知/死信证据、通知幂等、回调去重和 Outbox。文件日志轮转交由部署环境负责。

SQLite/MySQL 事务与幂等、CI Redis 发布/消费/ACK/重复消息及日志保留测试已编写，未执行。

## E. Web 管理

新增 Dashboard、测试器、历史与详情、尝试/审计日志、来源/API Key、目标页面。测试请求经可靠队列，标记 `is_test` 并记录审计；默认 QQ active。支持状态筛选、重试、重发、取消、批量重试。API Key 明文只在创建/轮换返回，后续遮蔽。

新增 QQ、SMTP、Webhook 配置；富文本渲染使用允许列表清理和安全 URL。前端会话恢复及 sanitizer 回归测试已写，未执行；浏览器 UI 验收未运行。

## F. 静态检查结果

| 检查项 | 结果 | 说明 |
|---|---|---|
| Go 格式检查 | PASS | 全仓库 gofmt，解析与格式检查通过；包含旧文件格式规范化 |
| Go Vet | SKIPPED | 会加载/编译项目，遵守禁止本地编译要求，配置到远程 CI |
| Go Lint | SKIPPED | 本地无现成工具，未通过编译安装 lint 工具 |
| React ESLint | PASS | 0 errors，36 warnings，旧页面警告保留 |
| TypeScript 检查 | SKIPPED | 当前为 JavaScript 项目，无 tsconfig |
| Git Diff 检查 | PASS | 无空白错误 |
| Workflow 检查 | PASS | actionlint 与 YAML 解析；OpenAPI 引用/路径参数静态检查 |
| Dockerfile Lint | SKIPPED | 本地无 hadolint；已配置远程 CI |
| 依赖检查 | PASS | go mod tidy -diff 无变更、go mod verify；npm 锁文件及 ignore-scripts 安装 |

本地单元测试、集成测试、前端构建、Go 编译、Docker 构建、服务启动、浏览器运行测试、真实 QQBot/SMTP/Webhook 发送全部未运行。原因：用户明确禁止本地测试与构建。依赖安装没有执行包安装脚本。

## G. GitHub Actions 准备情况

主工作流 `.github/workflows/docker.yml` 支持 push、PR、手动触发。配置静态检查、Go/前端远程回归和 Redis/MySQL CI fixtures，随后原生 `ubuntu-24.04` AMD64 与 `ubuntu-24.04-arm` ARM64 构建；不使用 QEMU，保留 SQLite 所需 CGO。

PR 与普通分支构建不发布；默认分支/tag push 通过检查与测试后用 `GITHUB_TOKEN` 的 `packages:write` 发布 GHCR，并按 digest 合并双架构 manifest。无需真实第三方凭证，测试使用 mock provider；真实平台验收仍另需机器人/SMTP 配置。旧重复 Docker 发布工作流已替换，旧独立 release/pages 收紧为显式手动执行。

当前未触发任何 Actions，未生成镜像或 digest，不能宣称构建成功。

## H. Git 状态

基线 Commit：`fbbfa58da1b65fc5d92fae083e06bec1701f8df3`。修改 85 个文件，新增 73 个文件，删除 2 个旧重复发布工作流。源码保留在未提交工作区；没有 commit、push、远程配置修改或 Actions 触发。

部署前需保存数据库备份和 ENCRYPTION_KEY；新版本加密旧通道与敏感选项，旧版本回滚需兼容备份，不能丢弃或任意替换密钥。旧弱默认 root 密码须在升级时旋转。现有部分旧平台适配器仍沿用原同步实现，其取消/超时行为需远程验证。

## I. 等待用户输入

源码改造及静态检查已完成。本地未执行任何测试或构建。请提供需要推送到的 GitHub 目标仓库地址和目标分支，我将继续完成 Git Push，并通过 GitHub Actions 进行远程验证与 AMD64 / ARM64 Docker 镜像构建。

按照原任务附件规定，第一阶段报告后停止，不擅自进入第二阶段。
