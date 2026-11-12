# GitHub 日期版本与 Docker Hub 发布

此分支的 Web/Docker 版本使用 `YYYY.M.D`，例如 `2026.11.12`，Git 标签为 `v2026.11.12`。这是独立 fork 的发布流程，不修改上游仓库，也不将日期版本自动用于桌面安装包。

## 1. 一次性配置

在 GitHub 仓库 **Settings → Secrets and variables → Actions** 中配置：

- Variable `DOCKERHUB_IMAGE`：`195658/deeix-chat`，不含 registry 前缀和 tag。
- Secret `DOCKERHUB_USERNAME`：具有目标镜像仓库推送权限的 Docker Hub 登录用户名。
- Secret `DOCKERHUB_TOKEN`：上述账号的 Docker Hub Access Token，需具备目标仓库写权限。不要填写 GitHub token，也不要写入 YAML、源码或发布说明。

GHCR 使用 Actions 自动提供的 `GITHUB_TOKEN`。Docker Hub 的 namespace 可以与登录用户名不同，工作流不会猜测目标仓库。未配齐 Docker Hub 配置时，Image 工作流会明确失败，不会只发布 GHCR 却显示为 Docker Hub 成功。

## 2. 准备版本

在发布分支更新根目录 `VERSION`，然后运行：

```bash
pnpm api:generate
node scripts/sync-version.mjs --check
node --test scripts/image-release.test.mjs
pnpm --filter @deeix/web test
pnpm --filter @deeix/web check
pnpm --filter @deeix/web build
pnpm --filter @deeix/api-contract check
```

后端还应运行 `make lint` 和 `go test ./...`。将同步后的 manifests、Cargo 根 package 版本和生成契约一起提交，保留锁文件中无关依赖不变。日期版本不自动生成桌面包，尤其不要用四位年份 major 直接发布 Windows MSI。

首次发布流程示例：

```bash
git switch -c sub2api-2026-11-12
# 审查并提交源码、版本、工作流和正式文档，不提交运行目录。
git push -u origin sub2api-2026-11-12
# 等待该分支的 CI 通过后：
git tag -a v2026.11.12 -m "Release v2026.11.12"
git push origin refs/tags/v2026.11.12
```

不要强制移动已发布 tag。常规用户 Git 凭据推送 tag 会自动触发 [Image 工作流](<../.github/workflows/image.yml>)。分支 push 只做 CI，不发布镜像。支持 `sub2api-*` 分支的 CI 已包含 Web 单测和镜像发布策略单测。

## 3. 镜像标签与平台

稳定 tag push 会发布以下多架构标签，均包含 `linux/amd64` 与 `linux/arm64`：

```text
195658/deeix-chat:v2026.11.12
195658/deeix-chat:2026.11.12
195658/deeix-chat:latest
195658/deeix-chat:sha-<完整提交SHA>-<运行编号>-<重跑编号>
```

GHCR 的 `ghcr.io/kiritoko1029/deeix-chat` 同步发布相同标签。两个原生架构 runner 独立构建、上传运行级暂存标签，合并 job 完成后逐一检查两个平台；不会从不同运行的临时标签混合镜像。

- `-rc.1` 等预发布版本不会更新 `latest`。
- 手动 `workflow_dispatch` 只能选择与 `VERSION` 匹配的 tag，且不会更新 `latest`。
- 分支、PR 或 tag/版本不一致不能绕过预检发布。
- 发布串行执行，但不做版本号大小排序；不要在新版本发布后重跑旧稳定 tag 来覆盖 `latest`。
- 需要固定内容时使用 digest；Git/镜像 tag 不等同于不可变的内容地址。

## 4. 发布 GitHub Release

镜像构建和 GitHub Release 是两个步骤。确认 CI、Image 工作流及 manifest 成功后再发布 Release，例如：

```bash
gh release create v2026.11.12 \
  --repo kiritoko1029/DEEIX-Chat \
  --verify-tag \
  --title "v2026.11.12 — sub2api 接入与多 Key" \
  --notes-file docs/releases/2026.11.12.md \
  --latest=false
```

本轮 Web/Docker Release 不包含桌面 updater 文件，因此不提升为 GitHub 全局 latest，避免影响使用 `releases/latest/download/latest.json` 的桌面更新检查；这不影响 Docker Hub 的 `latest` 标签。日期 Release 也跳过 Desktop Channel 的滚动清单更新。

## 5. 验证与故障恢复

```bash
docker buildx imagetools inspect 195658/deeix-chat:2026.11.12
docker pull 195658/deeix-chat:2026.11.12
```

如果 tag push 因凭据或临时网络错误失败，在修复配置后选择原次运行的 **Re-run all jobs**，以重新构建两种架构并补齐稳定 `latest`。新开一次手动 dispatch 不会推广 `latest`。跨 GHCR/Docker Hub 无法实现事务性发布；若合并失败，部分标签可能已存在，以工作流日志及 manifest 检查为准，不能仅凭 Release 页面存在就判断成功。

不要上传本地数据库、配置、token、日志或 `.dsh` 报告作为 Release 附件。正式部署仍需自行配置安全密钥、持久化卷、可信 sub2api 地址及个人模型服务开关，见 [接入说明](<SUB2API.md>)。
