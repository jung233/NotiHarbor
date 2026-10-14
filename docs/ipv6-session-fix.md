# IPv6 与 Session 静态审计

基线：`fbbfa58da1b65fc5d92fae083e06bec1701f8df3`。

## 已发现的问题

原 `main.go` 忽略 `redis.NewStore` 错误。原 `common/constants.go` 每次进程启动生成随机 SessionSecret，未配置 SESSION_SECRET 时重启或多实例必然使既有签名失效。原 Cookie 未配置 SameSite/Secure，Gin 默认可信代理未显式收紧。原身份中间件对会话字段直接做类型断言，异常字段可能引发 panic。

这些是实际源码证据，但不能证明随机密钥或 IPv6 冒号造成 Issue #185 的 Base64 非法字符。正确 SecureCookie 编码不会因客户端 IPv6 地址出现冒号而失败；原认证代码没有把 Session 绑定客户端 IP。非法 Base64 更可能涉及损坏/截断/代理改写 Cookie 或同名 Cookie 冲突，需运行环境证据确认。

## 修改

- 强制至少 32 字节稳定 SESSION_SECRET；禁止随机启动密钥。
- Session Store 初始化失败直接终止，不使用无效 store。
- Host-only、HttpOnly、Lax Cookie；HTTPS 公共 URL 默认 Secure，不接受转发头直接决定安全属性。
- 显式 TRUSTED_PROXIES；未配置时不信任转发头。
- 同名 Session Cookie 拒绝认证并清理当前 Host-only Cookie。
- 类型安全的身份检查；签名错误 Cookie 不产生用户身份。
- Cookie Session 可通过 SESSION_STORE=cookie 与 Redis 队列同时使用。
- Redis Session 支持 URL 选择逻辑 DB，并使用 message-pusher:session: 键前缀；旧驱动不支持 ACL username 或 TLS。不支持的连接配置 fail-fast 并建议使用 Cookie Session，不会静默改用其它存储。
- 同源 Origin/Referer 检查保护 Cookie Session 修改请求与登录/退出；请求体上限 1 MiB。

凭证变化后旧 Cookie 被拒绝是预期行为，不能跳过签名校验。反向代理 HTTPS 部署必须设置 APP_PUBLIC_URL，域名不设 Cookie Domain；IPv6 字面量 URL 使用方括号。

## 回归测试与证据等级

`middleware/session-security_test.go` 编写了 IPv4/IPv6 地址切换、同密钥 Store 重建、密钥变化、非法/同名 Cookie、HTTPS Cookie 属性及 `/api/user/self` 用例。它们是 HTTP mock，不是真实双栈网络。

NOT EXECUTED — LOCAL TESTING PROHIBITED。

需要远程 CI/部署后补充验收 Redis 真实会话、Cookie 到期、完整登录退出、代理 Set-Cookie/Header 保真、真实 DNS AAAA/A 网络切换及浏览器恢复行为。请对故障环境采集脱敏 Cookie 长度/重复名称、代理配置与实例密钥配置一致性；不要采集完整 Cookie 或 Secret。

**已修复发现的相关代码缺陷，但原始 IPv6 故障仍需远程运行验证。**
