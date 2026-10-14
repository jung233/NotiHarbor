# SMTP 邮件通道

`email` 通道可沿用系统 SMTP，也可为单个通道配置 SMTP 服务器。邮件内容支持纯文本和 Markdown 渲染后的 HTML；两种正文同时启用时会发送 `multipart/alternative`。

## 系统默认 SMTP

管理员在系统设置中填写 SMTP 主机、端口、账号和访问凭证。账号凭证由系统设置安全存储。新增用户的邮箱地址用于默认收件人。

## 单通道 SMTP

在通道字段中配置：

| 字段 | 内容 |
| --- | --- |
| `url` | SMTP 主机名，不带协议、斜杠或路径，例如 `smtp.example.com` |
| `app_id` | SMTP 用户名 |
| `secret` | SMTP 密码或应用专用凭证；存入加密凭证字段 |
| `account_id` | 可选 SMTP 端口，例如 `587` 或 `465` |
| `other` | 连接、安全和正文选项 JSON |

```json
{
  "tls_mode": "starttls",
  "timeout_seconds": 30,
  "html": true,
  "from_name": "NotiHarbor",
  "from_address": "notify@example.com"
}
```

端口 465 默认使用直接 TLS，其他端口默认使用 STARTTLS。`tls_mode` 允许 `tls`、`starttls` 或 `none`；`none` 只适用于明确允许明文且不使用 SMTP AUTH 的受控服务器。TLS 最低为 1.2，并验证服务器证书。配置用户名时必须同时提供密码，认证只在 TLS 建立后执行。

`timeout_seconds` 可设 1 到 120 秒，省略时为 30 秒。`html` 默认开启；设为 `false` 时只发纯文本。`from_name` 与 `from_address` 可选，后者需是有效邮件地址。未填写单通道 SMTP 字段时会使用系统默认 SMTP。

## 收件人和权限

未指定消息收件人时发送到当前用户绑定的邮箱。消息的 `to` 可指定多个收件人，使用逗号、分号或竖线分隔。向其他收件人发送仍受用户的 `send_email_to_others` 权限控制。适配器不会更改用户邮箱或源消息对象。

## 失败处理

SMTP 4xx 临时响应可重试，5xx 响应和无效地址为终止错误。DNS、连接和 TLS 错误独立分类。DATA 阶段超时或连接断开可能已经交付，会标记为未知结果和已尝试发送；队列应避免盲目重发。连接、认证和收件人预检失败不会计入平台消息发送尝试。
