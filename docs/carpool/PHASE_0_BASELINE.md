# 阶段 0：环境、原版基线与认证审查

日期：2026-09-16（Asia/Shanghai）

> 历史快照：本文记录功能修改前的原版结果，以下“未执行/待确认”均指基线检查时刻。用户随后确认了双站设计，修改后的 15 组本机 E2E 与回归结果见 `PHASE_0_AUTH_RESULTS.md`，当前状态以 `HANDOFF.md` 为准。保留本文原始失败记录，不将后续结果回填为原版通过。

> 后续收尾：两项前端基线失败已修复，最终全量 2163 项通过，见 `PHASE_0_FRONTEND_FIXES.md`；认证报告最终为 16 组实验（增加修改版默认 A 兼容性）。本文中的原版失败数字不变。

## 结论与边界

阶段 0 **尚未验收完成**。原版前后端可构建，后端单元测试、集成测试及 lint 通过；前端 CI 关键集通过，但全量前端测试存在 2 项可复现的基线失败。

已完成 OIDC、身份绑定、用户/管理员鉴权、API Key、鉴权缓存、支付履约及迁移加载器的代码审查。已有认证专项测试通过，不代表已完成双站浏览器 SSO 或车辆权限验收。A/B 实例和身份服务尚未搭建，本文末尾是待确认的最小验证建议。

本轮没有修改业务代码、测试代码、依赖清单或锁文件，没有提交、推送、触发远程 CI、购买服务器、接真实支付、导入上游凭证或部署生产服务。保留了进入任务前已有的 `.gitignore` 和 `docs/carpool/` 改动。

## 固定基线

| 项目 | 实测值 |
| --- | --- |
| 分支 | `carpool/main` |
| Commit | `881f3202694c6bc932446931a30c27d9675178b9` |
| Commit 说明 | `chore: sync VERSION to 0.2.5 [skip ci]` |
| origin | `https://github.com/CLccxx/sub2api-carpool.git` |
| upstream | `https://github.com/Wei-Shaw/sub2api.git` |
| 原有改动 | `.gitignore` 放行拼车文档；`docs/carpool/` 未跟踪文档 |

这是本地测试基线，不是已经选定或验收的生产版本。未读取、复制其他项目的账号或数据库内容。

## 开发环境

| 工具 | 检查与处理 |
| --- | --- |
| 架构 | macOS / arm64 |
| Node | `v20.20.2`，与 CI 的 Node 20 大版本一致 |
| pnpm | `10.33.0`；CI 配置为 9，本轮未更改全局版本，属于环境差异 |
| Go | 初始 PATH 缺失；下载官方 `go1.27.0.darwin-arm64.tar.gz` 并校验官方元数据中的 SHA-256，解压至 `/tmp/carpool-toolchain-1.27.0/go` |
| golangci-lint | 初始缺失；下载并校验发布资产 SHA-256，版本 `2.13.0`，使用 Go `1.27.0` 构建 |
| Docker | 客户端/服务端 `29.4.3`；初始守护进程未启动，经授权启动 Docker Desktop |
| Compose | `v5.1.4` |
| 本机 PostgreSQL | CLI `16.13`，未连接或改动本机已有数据库 |
| 本机 Redis | CLI `8.6.2`，未连接或改动本机已有实例 |
| 集成测试服务 | 原版 harness 使用独立 `postgres:18.1-alpine3.23` 和 `redis:8.4-alpine` 测试容器 |

Go 与 lint 只放在临时目录，没有修改 shell 配置或进行系统级安装。临时目录被系统清理后需重新准备工具链。不要把 DEV_GUIDE 中其他 fork、Windows 路径、示例密码当成本项目配置。

前端依赖通过 `pnpm --dir frontend install --frozen-lockfile` 安装。首次安装因沙箱 DNS 限制失败，授权联网后成功。pnpm 10 提示未执行 esbuild/vue-demi 安装脚本，但本轮构建和测试可运行；没有全局放开依赖脚本权限。

## 执行结果

以下结果来自未修改业务代码的本地执行。Go 测试均使用 `-count=1`，不是复用测试结果缓存。

| 检查 | 结果 |
| --- | --- |
| `pnpm --dir frontend run build` | 通过，含 i18n 检查、vue-tsc 和 Vite 构建 |
| `make test-frontend` | 通过，含 lint、类型检查、17 个测试文件 / 227 项测试 |
| `pnpm --dir frontend run test:run` | 失败：282 个文件通过、2 个文件失败；2161 项通过、2 项失败 |
| 单独复跑上述两个前端文件 | 同样 2 项失败，另 1 项通过 |
| `make -C backend build` | 通过，生成 `backend/bin/server`，版本参数 `0.2.5` |
| `go build -tags embed -trimpath -o bin/server-embed ./cmd/server` | 通过，生成包含前端资源的本地二进制；此命令未注入发布版本参数 |
| `go test -tags=unit -count=1 ./...` | 授权复跑后通过，56 个有测试的包返回 ok |
| `CI=true go test -tags=integration -count=1 ./...` | 通过，50 个有测试的包返回 ok，包括真实容器支持的 repository 包 |
| `golangci-lint run --timeout=10m ./...` | 通过，`0 issues.` |
| OIDC/JWT/API Key/缓存撤销专项 | 3 个包通过，92 个顶层测试通过；含子测试为 124，无失败或跳过 |
| 5 个部署脚本回归 | 全部退出 0，具体命令见下文 |
| 双站浏览器 SSO、成员授权、旧 Key 撤销 E2E | **未执行，不能标为通过** |
| 远程 CI、容器镜像构建、完整应用部署 | 未执行 |
| 真实支付、SMTP 投递、生产固定出口 | 按本轮范围不验证 |

后端单元测试首次在沙箱内执行时，httptest/miniredis 绑定本机端口被拒绝。该次失败是环境限制，日志单独保留；沙箱外复跑结果才作为基线结果。

`backend/internal/repository/integration_harness_test.go` 在 Docker 不可用且没有 `CI` 环境变量时会直接 `os.Exit(0)`，本轮设置 `CI=true` 防止这种整包跳过伪装成通过。原有个别 `t.Skip` 仍存在，例如 `concurrency_cache_integration_test.go` 的 TODO；命令成功不代表没有任何被跳过的用例。

部署脚本测试主要使用 mock/静态检查，不能作为真实应用部署成功的证据。运行的脚本是：

```sh
/bin/bash deploy/tests/apple-container-test.sh
/bin/bash deploy/tests/docker-compose-security-test.sh
/bin/bash deploy/tests/docker-compose-gateway-env-test.sh
/bin/bash deploy/tests/docker-runtime-resources-test.sh
/bin/bash deploy/test-caddyfile-cache.sh
```

构建存在原有的 Browserslist 数据过期、动态/静态 import 混用和大 chunk 提示；前端测试存在 router-link 等 stub 警告。本轮没有通过升级依赖或关闭断言掩盖这些提示。

### 前端基线失败

1. `frontend/src/views/admin/__tests__/ChannelMonitorView.grok.spec.ts:104`：断言 8 个 provider，实际为 10。`MonitorFormDialog.vue:471` 起已包含 minimax、opencode_go 等 10 个选项。属于断言与当前 UI 不一致，需要单独修正测试并回归。
2. `frontend/src/views/admin/__tests__/GroupsView.codexManifest.spec.ts`：挂载 GroupsView 时没有 active Pinia。`GroupsView.vue:4444` 使用 `useAuthStore()`，该测试只 mock 了 app/onboarding store，没有安装 Pinia 或 mock auth store。不是本轮引入的业务改动。

上述失败均单独复现，未修改它们；因此不能声称原版全部测试通过。

### 复现环境与日志

仓库根目录执行前端命令。后端命令在 `backend/` 内执行，先设置：

```sh
export PATH="/tmp/carpool-toolchain-1.27.0/go/bin:/tmp/golangci-lint-2.13.0-darwin-arm64:$PATH"
export GOPATH=/tmp/carpool-gopath
export GOCACHE=/tmp/carpool-gocache
export GOLANGCI_LINT_CACHE=/tmp/carpool-golangci-cache
export GOTOOLCHAIN=local
go version
golangci-lint version
go mod download
make build
go test -tags=unit -count=1 ./...
CI=true go test -tags=integration -count=1 ./...
golangci-lint run --timeout=10m ./...
```

专项命令（并不覆盖全部 OAuth provider 或全部负面场景）：

```sh
go test -tags=unit -count=1 -json \
  ./internal/handler ./internal/server/middleware ./internal/service \
  -run 'Test(OIDC|BuildOIDC|CompleteOIDC|TryOIDC|JWTAuth|AdminAuth|APIKeyAuth|ApiKeyAuthWithSubscriptionGoogle|GoogleAPIKeyAuth|AuthCacheInvalidation|AuthInvalidationRetry)'
```

本机临时证据文件，不应提交可能含测试令牌的原始日志：

- `/tmp/carpool-backend-unit.log`：被沙箱阻断的首次测试。
- `/tmp/carpool-backend-unit-unsandboxed.log`：成功的单元测试复跑。
- `/tmp/carpool-backend-integration.log`：集成测试。
- `/tmp/carpool-backend-lint.log`：lint。
- `/tmp/carpool-auth-baseline.jsonl`：专项结构化测试结果。
- `/tmp/carpool-frontend-baseline-failures.json`：两个失败文件的单独复跑。

## 认证与扩展缺口

### 身份登录不等于车辆授权

- `auth_oidc_oauth.go:377` 构造 provider type/key/subject，`auth_oauth_pending_flow.go:521` 按 OIDC issuer/subject 查本地身份，而非假定 A/B 用户 ID 相同。已有绑定、兼容邮箱选择和浏览器 pending session 路径。
- `auth_oidc_oauth.go:164`、`:300` 的 PKCE 和 ID Token 验证受开关控制。原版支持关闭它们；测试节点必须显式开启，不能照用兼容模式。
- `auth_oidc_oauth.go:982` 的 JWT parser 校验签名算法、issuer、audience、存在的时效声明及 nonce；但没有 `WithExpirationRequired()`，依赖 jwt/v5.3.1 默认允许缺失 exp。iat 也未作为必需字段检查。这是静态审查发现，需补负面测试后修复，不能把当前专项通过解释为 OIDC 校验完整。
- `auth_oidc_oauth.go:1286` 快捷路径将 access/refresh token 写入回调 URL fragment，前端也支持接收。fragment 不等于 HTTP query，但仍与本计划“不把长期令牌放进跳转链接”的约束不一致。建议改用现有浏览器绑定 pending session 完成交换，并测回调 URL 不含长期令牌。
- `auth_service.go:899` 的注册赠额计划包含余额、订阅和平台额度；OIDC 新用户可被创建为 active。仅配置统一身份不会自动限制到已购车辆。

### Key 与协议入口

- `routes/user.go:20` 使用 JWT/用户状态/后台模式守卫，`:79` 的 Key 管理路由没有车辆资格守卫。
- `api_key_service.go:461` 的 Create 校验用户存在、可选分组等，但没有车辆成员校验；不能只在前端隐藏按钮。
- `user.go:84` 默认允许未开启公开分组限制的用户绑定非专属分组。默认零余额或仅用专属分组都不能完整替代成员有效期、授权版本和租约。
- `api_key_auth.go` 和 Google 专用鉴权分别检查 Key/用户/分组及额度，但不理解 car/node membership；`routes/gateway.go` 多处协议入口均需覆盖，不应只测 `/v1/chat/completions`。
- 管理员认证支持全权限 Admin API Key 与管理员 JWT，不适合直接充当主站同步节点的窄权限凭证，更不能交给浏览器。

### 撤销、迁移与支付

- 已有 L1/L2 鉴权缓存、Redis Pub/Sub、持久化 invalidation outbox。`184_auth_cache_invalidation_outbox.sql` 对用户状态、Key、分组等变化排队失效；没有针对尚不存在的车辆成员表的触发/失效逻辑。
- 应复用已有失效机制，同时每个新请求检查成员状态和有效期，验证缓存预热、worker 重启、Redis 失效通知失败时不会无限延长权限。不能拿“禁用用户测试通过”代替“退款/到期旧 Key 失效”。
- `migrations_runner.go:123` 起只有一个嵌入迁移 FS/版本表入口，使用文件名排序、checksum 和 PostgreSQL advisory lock。拼车独立迁移目录/版本记录尚未实现；不得修改已执行的迁移，也不靠猜大编号隔离。
- `payment_fulfillment.go:216` 当前将订阅订单交给订阅履约，其余走余额履约。未来添加拼车订单必须显式分流；本轮没有连接任何支付渠道。

## 最小双站验证建议（待确认）

交接文档要求先确认设计再修改功能代码。以下不是已实现或已通过的内容。

两种路径：只运行原版双站，可验证 SSO 并复现授权缺口，但无法达到成员隔离验收；推荐运行原版对照后，增加最小节点授权扩展，再用同一套用例验证。暂不提前实现完整车辆、支付、通知或节点同步系统。

建议边界：

1. 本机 A、B 和一个标准 OIDC 测试身份服务，使用不同本地主机名（如 `a.localhost`、`b.localhost`、`id.localhost`），只绑定 loopback。不仅换端口，因为同主机的 OAuth cookie 名和路径相同。
2. 独立 A/B 数据库、Redis、JWT/TOTP 密钥、OIDC client/secret/回调；用同一浏览器会话验证 IdP 的免重复登录。身份服务具体镜像和版本在搭建时固定，不采购服务。
3. 只用虚构账号和本地上游 stub。用户一有 B 资格、用户二没有 B 资格；管理员使用独立身份。基于 issuer/subject 映射，不复制密码，不通过共享 JWT 密钥模拟 SSO。
4. 第一轮保留原版行为作为对照，记录未授权用户的建 Key/公开分组路径。第二轮通过测试夹具手工授予/撤销 B 的本地资格，验证缺省拒绝；不把夹具当作最终主站同步接口。
5. 最小授权扩展检查 node/car、状态、服务截止时间、租约截止时间和单调版本；对普通用户会话、建 Key、全部启用的模型协议入口统一生效。过期/暂停/撤销/不存在均拒绝；旧授权不能覆盖新撤销。
6. 撤销前预热缓存，撤销提交后旧 Key 的新请求必须被拒绝，且本地上游 stub 请求计数不能增加；在途流是否完成与新请求拒绝分别记录。断开 A/停止续租的测试使用人工租约夹具，不能声称已经验收真实同步补偿链路。
7. 最终证据包含浏览器步骤、HTTP 状态/错误码、身份映射与缓存撤销结果，但不记录明文 Key/密码。HTTP 的本机实验不代表生产 HTTPS、独立公网出口或真实上游调用已验收。

待执行矩阵：

| 场景 | 验收预期 | 本轮状态 |
| --- | --- | --- |
| A 登录后访问 B | IdP 不要求再次输入密码，B 自己签发会话 | 未执行 |
| A JWT/API Key 用于 B，以及反向使用 | 拒绝；独立签名/Key 存储，不跨站复用 | 未执行 |
| 无 B 资格的统一身份 | 无默认赠额，控制台业务接口/建 Key/模型请求拒绝 | 未执行 |
| B 有效成员 | 可自行建 Key，经 B 直接请求本地上游 stub | 未执行 |
| 普通成员访问管理接口/他人 Key 或使用记录 | 拒绝 | 未执行 |
| 错误 issuer/audience/nonce、缺失 exp、过期 token、回调重放 | 拒绝，不创建或绑定身份 | 未执行新增负面测试 |
| 缓存预热后撤销、暂停、到期、租约到期 | 旧 Key 新请求拒绝，上游计数不增加 | 未执行 |
| 旧授权版本在新撤销后重放 | 拒绝恢复权益 | 未执行 |
| B 与另一车辆节点 C 的隔离 | 后续增加第三实例再验收，不能由 A/B 实验替代 | 未执行 |

下一步先确认上述验证范围，并决定是否先在独立补丁中修复两项前端基线测试；随后按先测试、后实现执行。保留阶段 0 未完成状态，暂不进入收费或生产部署阶段。
