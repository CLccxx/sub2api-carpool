# 阶段 0：本机双站认证与权限验证

日期：2026-09-16（Asia/Shanghai）

> 同日后续更新：两项前端基线失败已仅通过测试夹具修复，全量前端 284 文件 / 2163 项通过，构建与关键集通过，见 `PHASE_0_FRONTEND_FIXES.md`。下文回归表中的“原版全量”保留历史结果，不代表当前仍失败。

## 结论

用户确认最小设计后，本机 A/B 实验通过 **16 组检查**。先用原版复现“统一登录即可在无车辆资格的 B 建 Key 并调用”的缺口，再启用默认关闭的节点成员门禁验证拒绝、授权和撤销。最后用修改版 A 验证关闭门禁时的已有会话、Key、OIDC 注册赠额保持可用。

这完成了本轮环境、原版构建测试和本机认证隔离范围，**不是阶段 0 全部验收或生产上线许可**。远程 CI、生产 TLS/出口、真实成员同步等仍未完成；原版两项前端失败已在后续收尾修复。没有接真实支付、真实模型账号、SMTP，没有购买或部署生产服务器，没有提交或推送。

基线：`881f3202694c6bc932446931a30c27d9675178b9`（0.2.5）；当前分支：`codex/phase0-node-access`。原版历史证据见 `PHASE_0_BASELINE.md`，运行步骤与配置见仓库 `tools/carpool/README.md`。

## 最终双站实验

- 运行命令：`CARPOOL_BASELINE_BINARY=/tmp/carpool-baseline-server node tools/carpool/lab.mjs`。
- 最终运行 ID：`carpool-p0-58bea1bc`；进程退出码 0，`results.json` 的 `status` 为 `passed`。
- 原版二进制：`/tmp/carpool-baseline-server`；修改版：`backend/bin/server-embed`。
- 本地证据目录：`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-c8BtkW`。
- `results.json` 为脱敏结果；`authorized.png`、`unauthorized.png` 已人工查看。已修正早期截图过早捕获空白/过渡动画的问题，最终截图显示仪表盘与拒绝提示。
- 原始日志、realm、配置含随机测试凭证，仅保留在私有临时目录，不入库、不发布。临时目录清理后需重跑。
- 退出后核查 `docker ps -a --filter label=carpool.phase0` 为空；未改动其他项目容器。未保留对外或本机常驻实验服务。

| # | 检查 | 结果与证据 |
| --- | --- | --- |
| 1 | 原版 A 登录后访问 B | 同浏览器 IdP 会话，无需第二次输入密码 |
| 2 | 统一主体映射 | 两个独立数据库绑定相同 issuer/subject，不共享用户库或 JWT 密钥 |
| 3 | 原版授权缺口对照 | 无车辆授权的 Bob 能建 Key 并成功到达本地模型 stub |
| 4 | 跨站凭证隔离 | A/B JWT 和 API Key 双向交叉使用均 401 |
| 5 | B 启用门禁后默认拒绝 | 原版已签发会话与已生效 Key 在无授权时被拒绝 |
| 6 | 无资格 OIDC 新用户 | 回调拒绝；数据库没有创建 outsider 账号 |
| 7 | 有效授权、重复事件 | 同版本相同内容幂等；B 直接到本地 stub，不经 A 转发 |
| 8 | 回调与 pending exchange | 回调 Location 无访问/刷新令牌；已消费 cookie 重放失败；授权后新建 Key 可成功调用 |
| 9 | 赠额、管理员、所有权 | 节点新用户 Carol 余额为 0；普通用户访问管理接口 403，他人 Key 404 |
| 10 | 缓存预热后撤销 | 旧 Key、用户 API、刷新与旧授权重放均失败；拒绝请求没有增加上游计数 |
| 11 | 多协议入口 | OpenAI responses、Anthropic messages、图片、Google 原生入口均拒绝撤销 Key |
| 12 | 暂停与过期 | 暂停、服务到期、租约到期均拒绝，未进入上游 |
| 13 | 更新授权恢复 | 更高版本有效授权恢复使用 |
| 14 | 在途流与新请求 | 已接纳 SSE 流正常到 `[DONE]`；撤销后新请求拒绝 |
| 15 | 无新事件的自然过期 | 短租约自然过期后拒绝，不依赖新同步事件或缓存失效通知 |
| 16 | 修改版默认 A | 门禁关闭时原 JWT/Key 继续有效，新 OIDC 注册、原赠额和新 Key 调用可用 |

授权截图中的 Alice 余额来自**原版对照阶段**的赠额，门禁不会追溯清空已有余额。零赠额断言针对启用节点模式后新注册的 Carol；即使旧用户仍有余额，撤销后也不能调用。

## 实现边界

- 新增 `backend/internal/carpool/`：独立 `carpool_schema_migrations`、校验和、迁移锁、本地成员表，使用 node/car/issuer/subject 联合键；不修改上游迁移文件。
- 本地授权 CLI 从 stdin 接收 JSON，仅接受已知字段与目标节点；最大租约 15 分钟，单调版本更新、相同内容幂等、旧版和同版本冲突拒绝。没有新增公网授权管理接口。
- 用户会话签发/刷新、JWT 用户接口、Key 创建、两条 Key 鉴权中间件接入门禁。允许结果不缓存，每次查询 PostgreSQL，数据库不可用返回 503；资格不满足返回 403。
- 门禁位于简化模式和计费豁免之前；节点配置本身要求 standard 模式。管理员控制台恢复入口保留，但管理员的模型 Key 不能绕过成员资格。
- 节点模式关闭本地邮箱自注册和注册/首次绑定/promo 赠额；OIDC 身份授权先于本地建号。
- OIDC 校验要求 exp/iat，拒绝未来 iat 和不匹配 azp；已有 issuer/audience/nonce 继续校验。已验证邮箱快捷路径复用浏览器绑定 pending exchange，URL 不携带令牌。
- OIDC 收紧和无令牌回调同时作用于关闭门禁的 A；不是所有行为都完全不变。宽松或不符合上述声明要求的 IdP 需要修正配置，不能回退关闭节点验证。

## 回归与修复记录

| 检查 | 结果 |
| --- | --- |
| 原版前后端构建、后端 unit/integration、lint | 通过，详见原版记录 |
| 修改后全量 `go test -tags=unit -count=1 ./...` | 最终复跑退出 0，57 个有测试包通过；`/tmp/carpool-node-unit-final.log` |
| 修改后 `CI=true go test -tags=integration -count=1 ./...` | 最终复跑退出 0，51 个有测试包通过；`/tmp/carpool-node-integration-final.log` |
| 注册顺序修复后完整 handler/service unit | 通过，`/tmp/carpool-auth-review-fix.log` |
| `golangci-lint run --timeout=10m ./...` | 通过，0 issues；`/tmp/carpool-node-lint-final.log` |
| 嵌入前端资源的 backend 构建、授权 CLI 构建 | 通过，最终实验使用已重建的修改版服务 |
| `make test-frontend` | 通过，lint、类型检查、17 文件 / 227 测试；`/tmp/carpool-node-frontend.log` |
| 原版全量前端测试 | 2161 通过、2 失败；没有修改前端或掩盖失败 |
| 最终 A/B 浏览器实验 | 16 组通过，见上表 |
| 远程 CI、镜像发布、生产或公网验证 | 未执行 |

两项原版前端失败：`ChannelMonitorView.grok.spec.ts` 期望 8 个供应商而实际为 10；`GroupsView.codexManifest.spec.ts` 缺少活动 Pinia/auth store 初始化。它们不是本次授权改动引入的失败。

关键红绿验证：

1. `/tmp/carpool-auth-red.log` 记录原版接受缺失 exp、缺失/未来 iat、错误单 audience azp，以及成功回调 URL 含令牌的失败；对应修复后通过。该原始测试日志含虚构令牌，不应公开。
2. `/tmp/carpool-identity-red.log` 记录真实 PostgreSQL 下绑定身份 `verified_at=NULL` 的误拒绝。上游 OIDC 绑定不保证填写该字段；已移除错误假设，保留绑定 issuer/subject、有效用户与成员资格检查，集成测试固定覆盖 NULL 情况。
3. 独立审查发现补填邮箱注册在身份绑定前生成令牌。新增 `TestNodeCreateOIDCOAuthAccountBindsBeforeIssuingTokens` 先复现有效成员预期 200、实际 403，再修正为先检查 pending 身份授权、建号、提交绑定、最后签发门禁会话。测试覆盖无资格不建号、有资格成功、绑定期间撤销不发令牌。已提交绑定后若资格失效，不删除合法绑定；恢复资格后可重新登录。
4. 未保留最初 policy-only 失败运行的独立日志，不把最终绿灯当作这一步历史证据。

集成测试使用 `CI=true` 防止 Docker 不可用时上游 repository harness 直接退出成功。报告记录命令退出与包结果，不宣称上游所有条件性 skip 都已执行。

独立只读审查发现的注册顺序问题已修复；增量复审没有新的 P0/P1/P2 发现。节点注册的提交失败和提交后刷新令牌存储失败仍缺专门的节点模式故障注入测试，不能把已有默认模式回滚测试当作这两项全覆盖。提交后签发失败时保留绑定并消费 pending 会话，需要重新登录。

## 未验证与后续工作

- 只有 A/B，没有独立车辆节点 C 的实机交叉隔离；wrong-node/car 的单元测试不能替代三站 E2E。
- 授权由本地 CLI 夹具写入，没有 A 到 B 的窄权限同步接口、投递、重试、审计和断线恢复补偿。自然租约过期测试不等于真实同步恢复测试。
- 不承诺撤销中断已接纳请求；当前边界是撤销提交后新鉴权拒绝，已在途请求可以完成。
- 没有成员允许缓存，换来每次鉴权的数据库查询；生产容量、延迟、连接池和故障压力测试未做。
- 全新零余额节点成员可能仍受上游原有分组/订阅计费限制。实验成功调用使用原版已有余额的 Alice 和人工种植的合成分组，不代表收费套餐与成员额度已实现。
- 没有真实模型服务、生产 DNS/TLS、固定独立公网出口、SMTP、支付与退款调用、服务器购买、生产镜像发布或远程 CI。
- 本机模型与价格源指向 stub，浏览器外网请求拦截，但后端原版元数据/版本后台任务未进行网络级封锁；不能宣称完全离线或所有自动更新外联已禁用。
- 未实现正式成员失效页面；当前浏览器拒绝沿用回调错误提示，显示 `NODE_MEMBERSHIP_REQUIRED`。
- 两项前端基线失败已在 `PHASE_0_FRONTEND_FIXES.md` 中单独处理；当前所有改动保持本地未提交，应用 go.mod/go.sum、前端 package/lock 未改，仅实验工具有独立锁文件。
