# NotiHarbor 单容器部署

Compose 启动一个以非 root 用户运行的 NotiHarbor 应用容器，并连接同一 Docker 网络上的 MySQL 与 Redis。Compose 不创建或管理这两个依赖服务，也不负责数据库备份。网关异步投递依赖 Redis；生产环境请使用受保护、持久性符合业务要求的 Redis。

## 准备并启动

1. 准备 Docker Compose v2、MySQL 和 Redis；在 MySQL 中创建应用数据库与有权执行 schema migration 的账号。
2. 把应用、MySQL 与 Redis 连接到同一个 Docker 网络。
3. 复制 `.env.example` 为 `.env`，替换 MySQL/Redis 凭据、公开 URL、会话密钥、加密密钥和初始管理员密码。
4. 确认数据库和 Redis 主机名可从该网络解析。创建数据目录并授予容器 UID/GID `10001` 写权限。
5. 从当前 checkout 构建并启动应用，确认健康和就绪端点。

```bash
cp .env.example .env
docker network create notiharbor-net
mkdir -p data
sudo chown 10001:10001 data
docker compose up -d --build
docker compose logs -f notiharbor
curl --fail http://127.0.0.1:3000/healthz
curl --fail http://127.0.0.1:3000/readyz
```

`.env.example` 默认使用本地镜像标签 `notiharbor:local`，Compose 会从仓库中的 Dockerfile 构建。若改用预构建镜像，请只填写仓库实际发布的 tag 或 digest，并使用 `docker compose pull notiharbor && docker compose up -d notiharbor`；本示例不预设已经发布 GHCR 镜像。

`.env.example` 中的 MySQL 和 Redis 主机名是网络示例。将现有服务连接到 `notiharbor-net`，并按实际服务名称与密码编辑 `.env`。如果省略 `SQL_DSN`，应用使用绑定到 `/data` 的 SQLite 数据库；单实例可采用 SQLite，多实例部署应使用 MySQL。Redis 仍需可用，网关 worker 才能处理通知。

应用端口只绑定至宿主机 loopback。使用 HTTPS 反向代理对外提供服务，把原始 Host 和必要的代理头正确传递给应用。只将实际代理的固定 IP 或 CIDR 写入 `TRUSTED_PROXIES`。

## 环境变量

| 变量 | 用途 |
| --- | --- |
| `NOTIHARBOR_IMAGE` | 本地构建时默认为 `notiharbor:local`；如改用镜像，填写实际可用的 tag 或 digest |
| `DOCKER_NETWORK` | 已存在且同时连接 MySQL、Redis 的外部 Docker 网络 |
| `DATA_DIR` | 宿主机持久化目录；保存 SQLite 文件和应用日志 |
| `HOST_PORT` | loopback 绑定的宿主机端口，默认 `3000` |
| `SQL_DSN` | 可选 MySQL DSN；省略时使用 SQLite |
| `REDIS_CONN_STRING` | Redis URL；Redis 不可用时网关 worker 无法正常工作，`/readyz` 返回 503 |
| `SESSION_SECRET` | 稳定的随机会话签名密钥，至少 32 字节 |
| `SESSION_STORE` | 设为 `cookie` 可把 Session 放在签名 Cookie 中，同时将 Redis 留给队列；Redis Session 支持逻辑 DB 选择 |
| `ENCRYPTION_KEY` | Base64 编码后恰好 32 字节的 AES-GCM 凭据加密密钥 |
| `APP_PUBLIC_URL` | 用户访问的规范 URL；HTTPS URL 会要求 Secure Cookie |
| `COOKIE_SECURE` | `true`/`false`；HTTPS 公网 URL 不允许设为 `false` |
| `COOKIE_SAMESITE` | `lax`（默认）、`strict` 或 `none`；`none` 需要 Secure Cookie |
| `TRUSTED_PROXIES` | 受信代理 IP/CIDR 列表，逗号分隔；无代理时留空 |
| `INITIAL_ADMIN_USERNAME` | 首次初始化创建管理员的用户名 |
| `INITIAL_ADMIN_PASSWORD` | 首次初始化管理员密码，至少 12 个字符 |
| `SQLITE_PATH` | SQLite 文件路径；容器固定为 `/data/message-pusher.db`，保留既有数据文件名 |
| `PORT` | 容器监听端口；Compose 固定为 `3000` |
| `WORKER_CONCURRENCY` | Redis 队列 worker 并发数，默认 4，允许 1–128 |
| `DELIVERY_LOG_RETENTION_DAYS` | 旧的、已被后续尝试取代的终态投递尝试保留天数，默认 90；0 禁用 |
| `AUDIT_LOG_RETENTION_DAYS` | 审计记录保留天数，默认 365；0 禁用 |

先生成密钥，存入密码管理器或密钥管理系统，再写入 `.env`：

```bash
openssl rand -base64 48  # SESSION_SECRET
openssl rand -base64 32  # ENCRYPTION_KEY
```

环境文件包含数据库、Redis 和加密密钥，只允许部署管理员读取。不要提交 `.env`，也不要把密钥写进镜像或日志。`.dockerignore` 会排除本地 `.env` 文件。

`SESSION_STORE=cookie` 适用于带 Redis ACL 用户名或 TLS 的 Redis URL；Redis Session 支持 URL 指定的逻辑 DB，并使用 `message-pusher:session:` 键前缀。Session Cookie 与 Redis 队列是独立功能。

启用 Redis worker 时，SQL 日志保留任务每小时最多分别清理 500 条投递尝试和 500 条审计记录，具体数据范围见[队列和重试](queue-and-retry.md)。容器文件日志由宿主机日志驱动或外部轮转工具管理。

## 容器和网络边界

容器默认以 UID/GID `10001` 运行，只需对持久化 `/data` 有写权限。镜像保留 CGO SQLite 驱动，并使用 Debian/glibc 运行时。工作流配置了原生 `ubuntu-24.04` amd64 与 `ubuntu-24.04-arm` arm64 构建，不使用 QEMU；实际构建是否成功以对应 Actions 运行结果为准。

`/healthz` 是进程存活检查；`/readyz` 检查网关、SQL 与 Redis 是否可用。反向代理可用 `/readyz` 判断流量就绪。Compose 不直接向公网发布应用端口。
