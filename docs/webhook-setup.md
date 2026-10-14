# 自定义 HTTPS Webhook 通道

自定义通道对固定 HTTPS URL 执行一次 JSON `POST`。适配器在发送前解析并检查 DNS 结果，连接时重新解析并直接拨号到已验证 IP，同时保留 URL 主机名用于 TLS 证书校验。请求不使用系统 HTTP 代理。所有重定向均拒绝。

## 字段配置

通道 `url` 是 webhook 完整地址。不要在 URL 用户信息或查询参数中放凭证；`Secret` 是唯一的自定义通道敏感凭证字段，控制器应加密保存并在发送时提供明文给适配器。旧配置仍可把请求 JSON 模板直接写入 `other`，默认只将 HTTP 200 视为成功。

需要 headers、auth 模板、自定义成功码或内网目标时，将 `other` 写为如下对象：

```json
{
  "body_template": "{\"title\":\"$title\",\"description\":\"$description\",\"content\":\"$content\",\"url\":\"$url\",\"to\":\"$to\"}",
  "headers": {
    "X-Workspace": "alerts"
  },
  "auth_header": "Authorization",
  "auth_template": "Bearer {{secret}}",
  "success_status_codes": [200, 201, 202, 204],
  "timeout_seconds": 10,
  "allow_private_network": false
}
```

请求体中的模板变量为 `$title`、`$description`、`$content`、`$url`、`$to`。`body_template` 渲染后必须是合法 JSON，最大 1 MiB。普通 header 可放在 `headers` 中；名称、值会验证并拒绝 CR/LF 注入，Host、Content-Length、Transfer-Encoding、Connection 和 Proxy-Authorization 等受限头不能覆盖。

若要发送认证值，使用 `auth_template` 或敏感 header 值中的 `{{secret}}` 占位符。默认认证头是 `Authorization`，也可用 `auth_header` 指定其他 header。敏感 header 名称（例如 Authorization、API key、token、password）必须引用 `{{secret}}`，不能把凭证明文放进 `other`。占位符从 `secret` 字段取值；适配器不会记录或回显渲染后的 header。

`success_status_codes` 只允许 200–299，最多 20 项；省略时，新配置接受所有 2xx。`timeout_seconds` 为 1–120 秒，省略时 10 秒。3xx 永远不是成功，且不会跟随目标地址。

## SSRF 边界和内网授权

默认只允许公网全局单播 IP。解析到环回、私有、链路本地、组播、未指定、保留/文档网段或云实例元数据地址时拒绝请求；DNS 返回的所有地址都必须合规。`allow_private_network` 默认 `false`，只有确实需要访问自有内网服务时才显式设为 `true`。启用后可访问 RFC1918、IPv6 ULA 和环回地址，但仍拒绝链路本地、组播、保留网段及常见云元数据 IP。URL 仍必须使用 HTTPS，证书校验保持开启。

若 DNS 解析失败或 DNS 答案不合规，适配器不会发起 HTTP 请求。响应重定向不会被跟随，避免主机从公网跳转到内部地址。

## 失败和重试

HTTP 429 和 5xx 可重试并保留 `Retry-After`；普通 4xx 是终止拒绝。收到的服务端响应会标记为已尝试发送。DNS、配置和拨号失败是发送前错误；请求发出后超时/连接中断可能已处理，会标记为未知结果与已尝试发送，队列不应盲目自动重发。Provider JSON 中仅提取短 `code`/`error_code` 字段，不保留完整错误正文。
