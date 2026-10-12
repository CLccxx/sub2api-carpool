# 阶段 2A：人工入车后端验证

实施：2026-09-18；收尾验证：2026-09-19（Asia/Shanghai）。范围由用户确认的 `PHASE_2_MANUAL_DESIGN.md` 与任务计划 `PHASE_2A_PLAN.md` 界定。本记录只覆盖后端，不代表 2B 页面或生产运营验收。

结论：阶段 2A 后端检查点通过。下一步为 2B 页面与浏览器联调，人工功能仍默认关闭。

## 实现范围

- 默认关闭的人工入车配置；与测试订单互斥，继续禁止主站与节点模式混用。公开 API 与 SSR 返回只读能力标识。
- 增量迁移 003：车辆/订单明确区分 test/manual；人工订单直接确认、无支付占位期限；独立收款、退款和幂等命令表。
- 管理员添加已注册有效用户，全额登记支付宝/微信站外收款。车辆、座位、订单、成员、收款、事件和幂等记录在同一事务内完成。
- 20 人竞争最后一席仅一人成功；同键重放返回原订单当前状态，同键改内容冲突；同渠道参考号永久去重。
- 退出、取消整车、未成团均保留收款并建立待退款记录。管理员另行确认已站外全额退款；平台不调用退款渠道。
- 满员只待部署；满员后退出进入 `needs_review`，保留原满员/交付时间，停止加人，不自动补位。
- 管理员订单/成员详情与本人收款摘要分开投影；普通用户不能查看内部备注、交易参考号和登记人。敏感命令正文不进入通用请求审计。
- 关闭新增人工入车后仍可查历史、退出及登记退款。测试 CLI 拒绝人工模式配置和人工订单。

无真实收款、退款、支付 SDK、渠道调用、SMTP、资源采购或生产部署。余额、订阅、B 成员授权和服务起止日期没有因人工入车而开通。

## 环境与执行记录

主机未安装 Go，先前临时工具链已清理。官方归档下载超时后，改用官方 `golang:1.27.0`：

```text
sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146
```

Docker 测试仅创建隔离 `postgres:18.1-alpine3.23` 数据库，使用虚构用户和模拟收款。仓库只读挂载，Go 模块/构建缓存使用 `/tmp/carpool-go-mod`、`/tmp/carpool-go-build`。未修改应用依赖清单或锁文件。

| 检查 | 结果与范围 |
| --- | --- |
| 实施前领域基线 | `internal/carpool` 单元及 PostgreSQL 集成通过 |
| 受影响单元回归 | carpool/config/handler/service/middleware/routes 和 test-order CLI 共 7 包通过；membership CLI 无测试文件 |
| 带 race 的 PostgreSQL 最终回归 | `internal/carpool` 24.642s、`internal/server/routes` 13.564s、`cmd/carpool-test-order` 2.394s，全部通过 |
| 最后补充契约测试 | `-race -tags=unit,integration -run 'Test(Carpool\|Manual\|Main\|TestOrder)'`，领域/service/handler/routes/CLI 全部通过 |
| 后端/CLI 编译 | 最后 staticcheck 修正后，server/test-order/membership 三个目标全部构建通过 |
| 全量静态检查 | CI 指定 `golangci-lint v2.13.0` 最终通过，`0 issues` |
| 差异空白检查 | `git diff --check` 通过 |

最后单元回归耗时：carpool 0.002s、config 0.546s、handler 38.345s、service 208.923s、middleware 0.051s、routes 8.749s、test-order CLI 0.020s。上述是受影响包集合，不是全后端所有包测试。

静态检查首次并发执行时容器以 137 退出且无检查结果，未将其算作通过。其后采用 `GOMAXPROCS=2 GOMEMLIMIT=2GiB GOFLAGS=-p=1`、`--concurrency=1 --timeout=10m`，发现并修正一项 QF1008；最终同配置重跑返回退出码 0 和 `0 issues`。

完整容器运行方式和 HTTP 请求约定见 `tools/carpool/PHASE_2A.md`。

## 边界证据

| 场景 | 测试/证据 |
| --- | --- |
| 默认关闭、互斥、只读配置 | `TestManualConfigurationIsolation`、配置加载测试、公开设置 API/SSR 测试 |
| 老数据升级 | `TestMainMigrationPreservesExistingNodeGrants`、`TestManualMigrationPreservesTestOrders`；迁移重复执行，旧订单/节点授权逐行不变 |
| 人工入车全流程、隐私、满员退出 | `TestManualEnrollmentLifecycle`，直接确认、无服务日期、本人隔离、needs_review、整车取消、全额退款 |
| 容量、跨渠道与空参考号 | `TestManualCapacitiesAndReferenceChannels`，2/3/4 座填满；不同渠道可使用同一参考文本，空参考号不伪装成外部交易去重 |
| 最后一席、数据库故障回滚 | `TestManualLastSeatAndRollback`，20 个竞争请求、单个满员事件、事件写入注入失败后无残留订单并可重试 |
| 业务拒绝回滚 | `TestManualBusinessRejectionRollsBackCommand`，验证保存点不会误提交命令写入 |
| 同键并发和历史凭据 | `TestManualSameKeyRaceAndPermanentReference`，8 次同键同请求得到同一订单，退款后旧参考号仍不可重用 |
| 时间、退款不可改写 | `TestManualRecordingTime`、`TestManualRefundValidationAndImmutableReplay`，拒绝非全额、未来时间、早于收款时间及缺少确认；重放不覆盖原记录 |
| 截止与锁等待、重启补偿 | `TestManualEnrollmentDeadlineAfterUserLock`，等待用户锁跨过截止时间后拒绝入车；只读显示待退款，重建服务后反复 sweep 仅持久化一条待退款 |
| HTTP 和权限 | `TestCarpoolManualHTTP` 使用真实 JWT 中间件/PostgreSQL；完整注册路由测试覆盖认证与 423 合规拦截 |
| 严格输入与审计 | `TestCarpoolManualStrictInput` 三种命令各覆盖 14 种输入；`TestCarpoolManualAuditOmitsFinancialDetails` 验证正文省略且保留操作者 |

## 复核中修正

先复现断言失败，再实施最小修正并回归：

1. 人工收款登记时间曾用事务开始时间，可能早于确认入车时间。新迁移的收款登记、退款申请与命令时间改用实际数据库时钟，旧迁移不改。
2. 在测试车上登记正式收款原返回 404，修为约定的模式冲突 409；关闭人工功能仍为 404。
3. 测试 CLI 曾忽略人工配置。现人工配置为真或无法解析时在连接数据库前拒绝，保留未配置/false 的测试行为。
4. 静态检查报告嵌入字段选择器 QF1008；使用等价的方法提升调用，未改变分页校验行为。

安全与事务复核在当前任务内进行，没有派生子代理。核对了新 API 的管理组挂载、用户字段投影、敏感审计正文省略、SQL 参数化、模式守卫、保存点以及唯一约束。普通用户的 Membership 中管理员扩展字段保持空值并省略。

## 未验收范围

- 阶段 2B 未实现：管理员搜索/收款表单、退出/退款确认 UI、联系管理员及「我的车」人工状态仍需开发。
- 本轮未重跑浏览器桌面/移动、A/B 登录授权 E2E、前端构建和测试；集成套件中的浏览器 harness 按显式开关跳过，不能计为通过。
- 不是全量后端所有包集成回归，不包括嵌入前端资源构建、远程 CI 或生产负载测试。管理员订单列表上限 100 条，目前每条再查详情，尚未做生产规模性能基准。
- 主站和新增人工入车均关闭/开启的行为不同：关闭新增只禁止新增登记，保留已发生记录和退款处理；关闭主站或节点模式不注册业务路由。两种入车开关都关闭时保留此前只读目录行为，2B 必须按单条记录模式渲染。
- 所有改动仍在当前本地工作区；保留阶段 0/1 未提交改动，不提交、不推送、不发布生产。仅已有阶段 2 设计独立提交 `91b403171` 保持不变。
