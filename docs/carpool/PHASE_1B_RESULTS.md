# 阶段 1B：页面与本机联调验收

日期：2026-09-16（Asia/Shanghai）

## 结论

**1B 已完成本地验收。阶段 1 的模型/API（1A）与页面/本机联调（1B）均已完成，但不代表支付或生产交付已完成。**

本轮新增管理员车辆页、登录后拼车大厅、“我的车”、中英文文案和移动端适配。仍使用测试订单；没有真实支付/退款、SMTP、节点授权同步、节点开通、生产部署、Git 提交或推送。保留全部此前阶段 0/1A 工作区改动，应用依赖与锁文件未变。

## 功能与边界

- `/admin/carpool`：筛选/分页，2/3/4 座草稿创建和编辑，整数分价格，招募/交付时间，发布确认、取消原因与已有成员二次确认、分页成员详情。
- `/carpool`：普通登录用户车辆列表、价格与规则详情、确认/占位/可用座位、发起/截止/交付时间、自己的有效参与状态、测试预约确认与冲突反馈。普通列表不展示他人成员身份。
- `/carpool/mine`：自己的订单快照、占位倒计时、取消、测试确认/过期/已取消状态与待部署成员。没有节点凭证、开通按钮或虚构的服务开始时间。
- 后端配置通过公开设置及 SSR 注入只读的 `carpool_main_enabled`、`carpool_test_orders_enabled`；默认关闭，B 节点关闭，前端导航与直达路由均按 opt-in 处理。关闭测试订单保留目录/管理/既有占位取消，但不能新增预约。
- 首次加载、空列表、失败/重试、重复点击、后台刷新及过期/满员/截止禁用态均有覆盖。用户页每 15 秒且页面可见时刷新，卸载清理定时器；数据库时间仍为最终裁决。

## 验收证据

| 检查 | 本轮结果 | 本地证据 |
| --- | --- | --- |
| 前端全量 `pnpm run test:run` | **289 文件 / 2232 测试通过** | `/tmp/carpool-phase1b-frontend-accepted.log` |
| 前端 `pnpm run lint:check` | 通过 | `/tmp/carpool-phase1b-lint-final-clean.log` |
| `pnpm run build`（包含中英文键检查及 vue-tsc） | 通过 | `/tmp/carpool-phase1b-build-accepted.log` |
| 嵌入资源后端与本地测试 CLI 构建 | 退出 0 | `/tmp/carpool-phase1b-server`、`/tmp/carpool-phase1b-test-order` |
| 功能开关/公开设置/路由/合规相关 unit/race | 4 包通过 | `/tmp/carpool-phase1b-backend-unit-final.log` |
| 核心 + HTTP + CLI PostgreSQL integration/race | 3 包通过 | `/tmp/carpool-phase1b-backend-race.log` |
| 后端 lint；integration 标签的 routes lint | 0 issues | `/tmp/carpool-phase1b-backend-lint.log`、`/tmp/carpool-phase1b-harness-lint.log` |
| Playwright + 真实 PostgreSQL/JWT/CLI | **15 组通过** | `/tmp/carpool-phase1b-browser-accepted.log` |
| 阶段 0 A/B SSO 与门禁回归 | **16 组通过** | `/tmp/carpool-phase1b-ab-final.log` |
| 中文交互预览 | 手机大厅与桌面管理员页面通过，无页面横向溢出 | `/tmp/carpool-phase1b-preview-user.png`、`/tmp/carpool-phase1b-preview-admin.png` |

浏览器覆盖 1440×1000、390×844，并用 360×800 检查只读手机页；已人工检查最终截图，管理员座位统计合并为一列避免交付时间被操作列遮挡，手机表单保存按钮位于固定底栏。

最终浏览器截图及脱敏结果目录：
`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-508174061`。

15 组场景包括：管理员创建已提交但响应丢失后的核对；桌面/移动建车、编辑、发布；预约已提交但响应丢失后的恢复与同键重放；过期占位禁用取消且可用新键重约；取消、重新预约、真实 CLI 测试确认；满员待部署、成员一致；有成员取消必须明确确认；关闭主站/B/只读开关；匿名登录跳转；无 B 授权、服务时间、余额变化或浏览器异常。

A/B 结果目录：`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-eqrYMg`。该回归运行于最后管理员纯前端异常恢复/文案/表格调整之前；后端鉴权代码此后未改，最终前端另经全量与浏览器复跑。未把早先的 A/B 运行称为最后前端构建的再次运行。

本轮未重复阶段 1A 的后端全仓 unit/integration 全套；本轮执行的是上述受影响包和拼车核心/HTTP/CLI 的 race 回归。1A 全仓证据保留在 `PHASE_1A_RESULTS.md`。

## 审查修复

1. 倒计时：旧/慢响应不能把已校准时间拨回。计入请求耗时、使用单调时钟推进；网络延迟采用保守估计，界面可能略早禁用，最终由后端裁决。新增 red/green 测试。
2. 预约幂等键：保存 user/car 对应的 key 与确认返回的 order ID；观察到匹配订单终态时才退休该次尝试，不能由较旧终态删除较新的未知请求键。
3. 管理员创建不具备后端幂等语义，因此网络/5xx 结果不明时禁止直接重新提交。核对必须回到第一页草稿，且只在列表请求成功、结果未过时、弹窗仍匹配时解除锁定。测试覆盖响应丢失及请求被覆盖。
4. 大厅补充满员后的实际 `delivery_due`；管理员成员弹窗明确标为测试订单；手机保存固定到底栏，发布和取消仍需确认。
5. 规格审查与代码质量审查均已关闭，无剩余发现。

初次浏览器启动曾因 PATH 缺 Node、Playwright 默认 headless shell 未安装而失败，已指定本机 Node 和 Chrome，不额外下载浏览器。一次并行 lint 恰遇 Vitest 临时配置文件删除（ENOENT），测试退出后单独重跑 lint 通过；不是通过忽略 lint 规则解决。构建仍有原有大 chunk / Browserslist 数据年龄提示，未为此升级依赖。

## 测试隔离与预览

浏览器正向管理员流程使用 integration-tagged 测试 harness：真实拼车 HTTP/事务和 JWT 中间件，身份仓储及其他 shell 接口使用虚构夹具。公开功能设置使用真实 handler/service。该 harness 不装载生产合规中间件，也没有接受声明接口或记录；生产路由的 423 合规拦截另由 `TestCarpoolAdminComplianceAndAudit` 验证。不能据此声称真实运营合规已验收。

本轮 UI harness 不需要 Redis，拼车事务只依赖 PostgreSQL；A/B lab 使用隔离 Redis 验证原节点行为。两套自动验证的进程/容器均已清理，其他项目资源未操作。

为用户查看页面，另行保留显式启用的本地交互预览，仅监听回环地址，带虚构 2/3/4 座车辆与测试成员，8 小时后自动停止。启动记录 `/tmp/carpool-phase1b-preview.log`；启动时地址为：

```text
管理员：http://127.0.0.1:65050/admin/carpool?preview_user=1
用户：http://127.0.0.1:65050/carpool?preview_user=2
```

同源链接会切换该浏览器的测试身份。预览初始化入口只存在于显式 opt-in 的 integration 测试代码，生产二进制不包含；没有真实账户和支付。保留的容器为本预览 PostgreSQL 及 testcontainers 清理器。重新启动会分配新端口，以日志为准，不能依赖临时路径永久存在。

复跑与预览停止说明：`../../tools/carpool/PHASE_1B.md`。

## 下一阶段

阶段 2 的支付/退款/邮件仍未实施。下一步可先做阶段 2 设计与支付沙箱方案评审；未经明确授权，不接真实商户、不购买服务器、不部署生产、不提交或推送远程。
