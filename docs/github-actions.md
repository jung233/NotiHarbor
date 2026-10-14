# NotiHarbor GitHub Actions 与 GHCR

.github/workflows/docker.yml 是容器镜像的唯一自动发布工作流。它在 pull request、分支 push、v* 标签和手动触发时运行静态检查与构建。测试使用 GitHub Actions 提供的临时 Redis 和 mock sender，不会访问真实消息渠道。

## 检查和构建

静态检查包括 Go module 下载和校验、gofmt、go vet、前端锁文件安装、ESLint、TypeScript（若配置）、Dockerfile lint 和 Action lint 1.7.12。仓库范围的 Prettier check 暂未启用：静态检查发现前端源码中有 59 个文件不符合仓库配置。静态检查 job 会生成 Go 嵌入需要的前端资源。

独立测试 job 在 Actions runner 上先运行前端回归测试（`CI=true npm test -- --watchAll=false`），再构建 Go embed 需要的静态资源并运行 Go 测试。它提供临时 Redis 7.4 与 MySQL 8.4 服务；Redis 会被 CI_REDIS_URL 门控测试使用；MySQL 通过 CI_MYSQL_DSN 被集成测试读取，覆盖 SQL outbox 原子性、幂等回放和冲突。另一个 Redis 集成测试验证 outbox 发布、mock sender、ACK 与重复 stream 事件不会重复发送。缺少对应 CI fixture 时这些测试会跳过。所有 sender 都是 mock，不会向真实渠道发送。

容器构建使用原生 Linux amd64 和 arm64 runner，不使用 QEMU。PR、手动运行和不可发布的分支 push 会运行无包写权限的双架构镜像验证；默认分支和版本标签 push 会运行双架构发布构建，manifest 发布需等待两个架构成功。

所有构建都使用锁定的 Go module checksum 与 npm lockfile。Docker build 使用 npm ci --ignore-scripts，不会把本机 node_modules 或 .env 放入镜像上下文。

## 发布条件和标签

GHCR push 只发生在：

- push 到仓库默认分支；
- push 一个版本标签。

Pull request、其他分支 push 和手动触发会验证镜像，但不发布镜像。手动触发本身不会绕过发布条件。默认分支生成 latest 与不可变 commit SHA 标签；所有可发布提交会生成 sha-<commit>；形如 v1.2.3 的 SemVer 标签额外生成版本标签。发布后输出摘要和架构信息。

同一 Git ref 的容器流水线通过 concurrency group 串行化，避免较旧构建晚完成后覆盖新标签。三个 legacy binary release workflow 已改为只接受 tag 上的显式 workflow_dispatch，并默认不上传；只有手动选择 publish_draft 才创建 draft release。它们使用同一个 tag concurrency group，防止并发改写同一个 draft。`github-pages.yml` 也只支持手动触发，限制从仓库默认分支部署，使用 GitHub 官方 Pages artifact actions 与最小 GITHUB_TOKEN 权限。旧的自动 Docker publication workflows 已移除。

工作流使用 GitHub 提供的 GITHUB_TOKEN。Fork PR 不接触发布 secrets；GHCR 发布权限只用于满足发布条件的 job。

## 运营建议

首次发布前确认仓库 Actions 权限允许工作流读源码并向 GHCR 写 package。私有 package 拉取需要主机有权限的只读凭证。生产部署可以使用 commit SHA 或 digest 固定镜像，而不是持续拉取 latest。不要在 CI 中配置生产渠道凭证。
