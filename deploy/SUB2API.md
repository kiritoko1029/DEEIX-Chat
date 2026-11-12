# sub2api 链接接入

此功能让**已登录 sub2api 的用户**通过链接进入 DEEIX Chat，免再次登录、免手工填写 API Key 和模型网关地址。它不是匿名访问，也不是关闭 DEEIX 的鉴权。运营方仍须完成一次可信实例配置。

## 1. 前提与边界

- 需要部署包含本功能的前端和后端。原版上游镜像不会因添加配置自动获得此功能。
- sub2api 管理站点和模型网关必须是公网 HTTPS 地址，DEEIX 后端须能访问。个人服务的 SSRF 防护不因开启本集成而关闭，内网/回环地址不可用。管理认证客户端不使用环境 HTTP(S) 代理，以免代理端 DNS 解析绕过公网拨号校验；仅能经代理访问管理站点的部署目前不支持。
- 用户已经在 sub2api 登录，且拥有可用的用户 API Key。集成**不会在 sub2api 自动创建新 Key**、修改分组、放宽额度或调用管理员接口。
- `token` 必须是 sub2api 面板登录返回的 **access token（JWT）**，不是 `sk-...` 模型 Key，也不是 refresh token。
- “上游”指 sub2api 对外模型网关，不是它内部管理的供应商账号或私有凭据。
- 普通用户的 Key 只导入该用户的个人模型服务，不能成为全站共享上游；sub2api 的管理员身份也不会自动获得 DEEIX 管理员权限。

## 2. 部署方一次性配置

在实际挂载的 `config.yaml` 中增加：

```yaml
sub2api:
  enabled: true
  base_url: "https://sub2api.example.com"
  api_base_url: ""
```

`base_url` 是可信的 sub2api **管理站点根地址**，不含 `/api/v1`。反代有路径前缀时保留前缀。不要填 DEEIX Chat 的地址，也不要根据链接里的 `src_host` 自动设置它。

模型网关 URL 的优先级：

1. 显式配置的 `api_base_url`；
2. sub2api `/api/v1/settings/public` 返回的 `data.api_base_url`；
3. 回退到可信的 `base_url`。

如果公开设置指向另一个 origin（协议、主机或端口不同），必须由部署方显式设置 `api_base_url`，而不是自动将 Key 发送给新主机。例如：

```yaml
sub2api:
  enabled: true
  base_url: "https://panel.example.com"
  api_base_url: "https://gateway.example.com"
```

对应环境变量为 `SUB2API_ENABLED`、`SUB2API_BASE_URL`、`SUB2API_API_BASE_URL`，环境变量覆盖 YAML。Compose 不会自动把宿主机上任意同名变量传入容器；使用环境变量方式时须在服务的 `environment` 中显式传递。

还需在 DEEIX 管理后台启用**个人模型服务**（运行时设置 `personal_provider:enabled`），并检查个人服务数量上限、主机黑名单和权限策略。本集成不会绕过这些设置。

如果这台 DEEIX 只用于用户自带 Key，建议使用后台默认的自用计费模式 `billing:mode=self`。`usage` / `period` 模式仍可能为同一请求中的平台收费服务（如 MCP 工具）预留预算；新建用户没有 DEEIX 余额或周期额度时，可能虽登录成功但无法开始对话。本集成不自动充值或绕过计费策略。

生产部署仍需设置正常的 DEEIX 安全密钥、公共 Web/API 地址及明确的 CORS origin 白名单；不要共用 sub2api 的 JWT 签名密钥。保持数据加密密钥及历史密钥可用，以便读取已加密的个人 API Key。

### 构建与启动

在仓库根目录构建本地镜像：

```bash
docker build -t deeix-chat:sub2api .
```

以 SQLite 部署为例，在 `deploy` 目录配置好 `config.yaml` 后运行：

```bash
DEEIX_CHAT_IMAGE=deeix-chat:sub2api docker compose -f docker-compose.sqlite.yml up -d
```

不要直接继续使用未经本次源码构建的 `ghcr.io/deeix-ai/deeix-chat:latest`。升级前备份数据库和加密密钥。

也可使用本分支的日期版本镜像，例如将 `DEEIX_CHAT_IMAGE` 设为 `195658/deeix-chat:2026.11.12`。首次拉取前确认 Image 工作流已经完成。自动构建、凭据配置和多架构标签规则见 [发布说明](<RELEASING.md>)。

## 3. 使用链接

入口是 DEEIX Chat 根路径，格式如下（`ACCESS_TOKEN` 代表当前用户的 sub2api access token）：

```text
https://chat.example.com/?user_id=1&token=ACCESS_TOKEN
```

如果能控制链接生成端，更建议使用 fragment，减少凭据进入首个 HTTP 请求和服务器访问日志：

```text
https://chat.example.com/#user_id=1&token=ACCESS_TOKEN
```

使用 `URL` / `URLSearchParams` 编码参数，不要将 token 写进长期静态菜单配置、公共文档、截图或日志。应由 sub2api 按当前登录用户动态生成。`user_id` 必须与 token 对应的用户一致，不能只更改 ID 访问他人账号。

前端在启动阶段将凭据暂存于内存并清理 URL，再通过 POST 交给 DEEIX 后端。后端调用固定 sub2api 实例的 `/api/v1/auth/me` 校验身份，随后读取 `/api/v1/keys` 和公开网关配置，探测模型并建立 DEEIX 标准会话。失败不会降级为匿名访问。DEEIX 本地二次验证若已启用，仍需完成。

普通不带参数的访问及原有登录方式不受影响。进入免登录链接表示开始账号切换：失败或等待二次验证时，不应自动恢复先前账号；用户可以显式使用普通登录。重复进入链接应关联同一 DEEIX 用户，而不是按邮箱或可猜测的用户名认领已有账号。

交换端点要求 `Content-Type: application/json`，只接受同源或明确允许的 CORS Origin；不可信跨站请求不能清除用户 Cookie。该接口始终按 IP/分钟限流，沿用 `auth:public_auth_rate_limit_rpm` 配额（默认 30），不受全局限流关闭影响；限流存储不可用时失败关闭。

### 昵称和邮箱同步

成功接入时读取 sub2api 的 `username` 和 `email`，清理并保存为独立的 `user.externalProfile` 展示资料。侧边栏、账号菜单及资料页优先展示远端昵称和邮箱；昵称为空时回退到有效邮箱，再回退默认名称。普通账号不受影响。

**展示邮箱不等于本地安全邮箱。** 同步不会修改 Chat 的密码找回邮箱、邮箱验证状态、角色或凭据，也不会按相同邮箱/用户名认领已有账号。内部随机用户名和“实例 + 远端用户 ID”映射保持稳定。已有接入账号无需重新注册，从 sub2api 菜单重新进入后即可补齐资料；仅刷新 Chat、使用本地 Cookie 续期不会重新抓取远端资料。

### 当前自动导入策略

对本次列表中的**全部符合条件的 Key 分别尝试导入**：先完整读取有界分页（最多 2,000 个 Key），核对所有返回 Key 的用户归属，再筛选 active、未过期、未耗尽总额度且分组 active 的兼容项。每个远端 Key 保留独立 provider ID 和模型引用，同名模型不会合并。

支持的分组平台与协议：

- `openai` → OpenAI Responses；
- `anthropic` → Anthropic Messages；
- `gemini` → Google Generate Content。

其他平台、未分组、停用、过期或额度耗尽的 Key 会明确标为跳过，不自动猜测协议。新 Key 会独立探测模型，首次导入每个服务最多启用 200 个发现的模型。服务自动命名为 `Key 名称 · 分组名称 · #Key ID`（最多 64 个字符且保留 ID）。在模型选择器中按所属 Key/分组手动选择；不会自动轮询、故障转移或切换你的已选模型。

一个 Key 探测失败不阻断其他 Key。接入结果提供“新导入／复用／跳过／失败”计数与逐项原因；至少一个服务就绪时可继续登录。没有兼容 Key 或所有导入均失败时，返回明确错误及摘要，不伪称同步成功。IP ACL、窗口额度和实际调用权限仍由 sub2api 执行，不自动创建远端 Key。

**数量和时间上限依然有效。** 个人服务上限沿用 `personal_provider:max_per_user`（默认 10，当前可设至 50），不会为导入而绕过。达到上限会逐项显示 `limit_reached`，需在后台调整限额或清理不需要的服务后从面板重新进入。导入最多 3 个并发，共享最多 25 秒预算，整个交换最多 50 秒；未完成项显示 `timeout`，重试复用已完成记录。配额紧张时成功占位取决于探测完成顺序，摘要按 Key ID 排序。

相同实例、用户和远端 Key 使用稳定的个人服务标识，重复登录不重复创建。旧单 Key 记录的默认名称会迁移为新名称，后续自动名称可跟随远端更新；用户手工改名、模型选择、图标、凭据和停用状态不会被自动覆盖。远端 Key 轮换、URL/协议变化需要通过已认证的个人服务管理处理，不静默覆盖密钥。

远端停用/过期的 Key 不计为本次可用成功；远端已删除、未再返回的 Key 不在本次摘要中。**历史本地服务不自动删除或停用**，因此可能仍出现在目录里，但真实请求继续由 sub2api 鉴权；这不是后台持续撤销同步。

**本地 2FA 限制：** 第二因素完成前只复用已有且匹配的服务，不新增 Key、不改名、不更新外部展示资料，新项标为 `two_factor_required`。本版没有第二因素完成后的延迟同步；此类账号的新增 Key 和资料更新需要后续专门的已认证同步流程，不能把 2FA 挑战结果视为“全部已同步”。

## 4. 外链与 iframe 不同

**本功能的默认使用方式是顶层页面跳转或新标签页打开。** sub2api 的“自定义页面”可能使用 iframe；仅拼好 URL 不代表 iframe 部署已可用：

- DEEIX 默认响应 `X-Frame-Options: DENY`，不允许嵌入。
- DEEIX Web refresh cookie 使用 `SameSite=Lax`；跨站 iframe 中可能无法保存或发送，首次登录后刷新也可能失效。
- 浏览器第三方 Cookie 限制、sub2api 的 CSP `frame-src`、反代安全头都可能阻断嵌入。

默认保持禁止内嵌。确需在受信 sub2api 面板中测试时，可显式配置 `sub2api.allow_embedding: true`（环境变量 `SUB2API_ALLOW_EMBEDDING=true`）并重启。仅当集成已启用且管理地址合法时，Web 页面使用 `Content-Security-Policy: frame-ancestors 'self' <管理站点origin>` 替代 `X-Frame-Options: DENY`；API/文件响应仍保留 DENY。允许来源只取部署配置，绝不读取链接里的 `src_host`，也不允许通配来源。设回 false 并重启即可撤销。

这是**受限的内嵌显示支持**，不是跨站 Cookie 或浏览器网络权限的绕过。HTTPS 面板内嵌 `http://127.0.0.1` 时，浏览器还可能要求本地网络访问权限；面板自身的 CSP 或 iframe sandbox 也可能限制加载。只在自己信任的面板上按需允许浏览器提示的本地访问，不要关闭全局浏览器安全策略。跨站环境若无法保存续期 Cookie，需要从面板重新打开携带新凭据的入口，或改用新标签页。正式部署推荐同主域 HTTPS，并验证实际浏览器策略。

## 5. 重要安全与会话行为

### sub2api 会话绑定

sub2api 新版有 `session_binding_enabled` 设置。启用后，登录 JWT 可能绑定用户 IP / User-Agent。浏览器的 token 交给 DEEIX 后端后，出口 IP/UA 改变可能触发 `SESSION_BINDING_MISMATCH`，甚至撤销原面板会话的 refresh token。

**上线前须确认此设置与实际部署版本。** 本集成不会伪造代理头或冒用客户端 IP 绕过绑定，也不建议为了接入默认关闭该安全功能。需要绑定时，应在 sub2api 侧增加专门的短时一次性授权码交换，再由双方服务端完成身份交接；这不是当前公开用户接口已提供的能力。

### URL 凭据风险

Query 中的 token 是完整面板凭据。地址栏清理不能擦除已进入 CDN、WAF、反向代理日志或父页面 iframe `src` 的值。请：

- 只把链接发送到自己信任并控制的 DEEIX 部署，不向第三方 Chat 分享；
- 尽可能使用 fragment，入口设置 `Referrer-Policy: no-referrer` 和 `Cache-Control: no-store`；
- CDN/反代不缓存带凭据的入口，不记录完整 query，不启用该入口的第三方分析；
- 不分享带 token 的链接；如曾在不可信位置暴露，应在 sub2api 撤销相关会话。

Nginx 可以对该站点使用不含 query 的访问日志（`log_format` 位于 `http` 块，`access_log` 位于对应 `server` 块）：

```nginx
log_format chat_no_query '$remote_addr $request_method $uri $status';
# server { ... }
access_log /var/log/nginx/chat-access.log chat_no_query;
```

不要使用包含 query 的 `$request` 或 `$request_uri` 记录登录入口。反代层和上游 CDN 都需检查，不能只修改应用日志。

### 生命周期

sub2api access JWT 仅用于此次身份核验，不保存到 DEEIX 数据库、浏览器持久存储或登录响应。模型 API Key 加密保存在个人服务中；DEEIX access token 按既有架构只存内存，refresh token 使用 HttpOnly Cookie。

DEEIX 会话、sub2api 面板会话及模型 Key 有各自的生命周期：面板退出或 JWT 过期**不会自动注销已建立的 DEEIX 会话**，也不是完整的联邦单点登出；需要同时撤销时应在两个系统分别处理。模型调用继续受 sub2api 的 Key 状态、有效期、额度、分组和用户权限约束。

## 6. 联调检查清单

建议使用专门的低额度测试用户，而非管理员日常账号：

- [ ] 已重新构建并部署前后端，开启集成并配置可信管理地址。
- [ ] 已启用个人模型服务，用户有可用 Key，目标域名未被管理员阻止。
- [ ] 已确认 sub2api 会话绑定不会阻止后端核验。
- [ ] 新用户通过有效链接进入聊天，模型列表可用，能发送一条测试消息。
- [ ] 重新进入不重复创建账号/个人服务；浏览器刷新仍能续期登录。
- [ ] 更改 `user_id`、过期 token、禁用账号均不能登录。
- [ ] 没有可用 Key、协议不支持、网关不可达时有明确失败，不创建共享上游。
- [ ] 已登录其他账号时进入链接不会显示或混用先前账号的聊天状态。
- [ ] 浏览器地址栏、访问日志、错误响应中没有 token 或明文 API Key。
- [ ] 不带参数的普通登录仍正常，DEEIX 二次验证不被绕过。

### 浏览器存储边界

账号切换隔离仅在 `sessionStorage` 保存非敏感的 `blocked` / `active` 标志，不保存 JWT 或 API Key。存储持续不可读或不可写时，客户端保守禁用自动续期，允许用户主动登录后在当前内存会话中使用。若标志未写成功、刷新后存储又恢复可用，则不能据此保证恢复此前的隔离状态；不同标签同时修改共享 Cookie、Safari/Firefox、BFCache 和真实桌面 keychain 故障仍需按部署环境验证。

## 7. 开发验证

已使用假上游和测试账号数据验证，不涉及真实面板凭据：

```bash
# 仓库根目录
pnpm --filter @deeix/web test
pnpm --filter @deeix/web check
pnpm --filter @deeix/web build
pnpm --filter @deeix/api-contract check

# backend 目录
go test ./...
go vet ./...
```

前端有 33 项 Node 测试，包含外部邮箱与本地安全邮箱隔离、不同 Key 同名模型引用、同步结果分类和 2FA 摘要交接；后端涵盖身份与 Key 归属、资料迁移、并发创建、配额、加密、2FA、Cookie、Origin、强制限流和出站限制。数据库集成测试使用临时 SQLite，尚未做独立 PostgreSQL 实例联调。

[浏览器回归脚本](<../apps/web/scripts/test-sub2api-browser.cjs>) 包含 18 个模拟 API 场景，覆盖普通入口、失败/网络中断后的旧账号隔离、2FA 返回 `/`、同文档新链接、旧异步结果、原生凭据调用桩，以及昵称/邮箱、部分成功、全部失败和 2FA 后的摘要展示。当前 18 项生产产物测试通过；原有 15 项认证场景也曾在 React StrictMode 开发模式通过。脚本需要单独提供已安装的 Playwright 模块和 Chrome，不会自动安装或访问真实 sub2api：

```bash
PLAYWRIGHT_MODULE=/absolute/path/to/playwright-core \
CHROME_EXECUTABLE=/absolute/path/to/chrome \
pnpm --filter @deeix/web test:sub2api:browser
```

## 8. 协议依据

本集成按 [sub2api 提交 530c5e3](https://github.com/Wei-Shaw/sub2api/tree/530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484) 的公开源码核实：

- [嵌入链接参数构造](https://github.com/Wei-Shaw/sub2api/blob/530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484/frontend/src/utils/embedded-url.ts)
- [当前用户认证路由](https://github.com/Wei-Shaw/sub2api/blob/530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484/backend/internal/server/routes/auth.go)
- [用户 Key 实际路由（`/keys`，不是 Handler 注释中的 `/api-keys`）](https://github.com/Wei-Shaw/sub2api/blob/530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484/backend/internal/server/routes/user.go)
- [公开网关配置](https://github.com/Wei-Shaw/sub2api/blob/530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484/backend/internal/service/setting_public.go)

不同部署版本、定制 API 前缀和网关协议应按实际实例核对。本仓库的自动化测试使用假上游，不代表已与您的线上实例联调。
