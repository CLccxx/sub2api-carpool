# 阶段 1A：后端实施与验收记录

日期：2026-09-16

## 结论与边界

**阶段 1A 模型与事务 API 已完成本地验收。阶段 1B 页面尚未实施，阶段 1 整体尚未完成。**

本轮在 `codex/phase0-node-access` 工作区完成，保留此前阶段 0 改动；没有提交、推送、接入真实支付/退款或 SMTP，没有购买或部署生产服务器，也没有导入真实上游凭证。

实现包括独立车辆/固定座位/测试订单/待部署成员/节点登记/事件表，数据库事务锁位、幂等确认、取消和到期清理，管理员及用户 API、配置开关、后台扫描生命周期和本地测试确认 CLI。接口与运行说明见 `../../tools/carpool/PHASE_1A.md`。

满员只进入 `awaiting_deployment`，成员保持 `waiting_deployment`，服务起止时间为空。节点仅保存登记元数据，不发起远程请求；不产生 B 授权、不执行原余额或订阅履约、不发送邮件。`car_full` 事件落库不代表邮件已发送。

## 验收矩阵

以下编号对应 `PHASE_1_DESIGN.md`。服务端测试通过不替代后续页面验收。

| 编号 | 本轮证据与结果 |
| --- | --- |
| P1-01 | validation 单测及 `draft visibility capacities and immutable terms`：2/3/4 座生成准确固定座位；非法容量和商品参数拒绝。 |
| P1-02 | 必填规则校验、发布后禁止修改、订单快照与非法状态测试通过。 |
| P1-03 | `last seat race and exactly one full event`：20 用户竞争最后一席，仅一个成功；数据库计数一致。 |
| P1-04 | 同键并发只产生一个订单，不同键同用户同车仍最多一席；跨车复用同键冲突。 |
| P1-05 | `idempotency ownership and user per car`：同用户可分别参加两辆车。 |
| P1-06 | 并发确认/重复确认不重复成员；满员事件唯一；重复扫描无重复副作用。 |
| P1-07 | `all held is not full and cancellation restores capacity`：全部 pending 仍为 recruiting，取消后计数恢复。 |
| P1-08 | 到期扫描与晚确认竞争、车辆及用户行锁等待后刷新数据库时间；过期订单不能复活或抢回座位。 |
| P1-09 | pending 取消与招募截止清理测试通过，订单/座位/成员终态和聚合计数一致。 |
| P1-10 | `cancel and confirmation are serialized`：最终确认与车辆取消串行化，取消之后不新增 full 事件；已有测试成员终结。 |
| P1-11 | 重新创建 service 后处理遗留到期记录，重复 sweep、启动扫描、停止等待测试通过。核心事务测试仅依赖 PostgreSQL，不连接 Redis；这不是整站 Redis 故障可用性验收。 |
| P1-12 | 注入成员及最终满员事件写入失败，证明座位/订单/成员/车辆/相关事件全部回滚。 |
| P1-13 | 真实 JWT 中间件与 PostgreSQL 的 HTTP 测试覆盖无凭证/坏凭证/旧 token、普通用户访问管理员接口、他人订单、注入 user_id 等；无 HTTP 确认接口。 |
| P1-14 | 主站关闭或 B 模式不注册主站路由；测试确认必须显式启用。主站开、测试订单关时保留目录/管理/已有 pending 取消，新增预约与确认返回 404。页面开关属于 1B。 |
| P1-15 | 满员仅待部署，服务时间为空；原余额不变、B 授权表不新增；节点登记不产生开通效果。 |
| P1-16 | 未实施，留给 1B 桌面/移动端完整流程及隐私展示验收。 |

补充迁移验证：从真正只应用 001 且已有有效节点授权的数据库升级至 002，重复迁移成功，原授权记录逐字段保持不变且仍可鉴权。001 文件未修改。

## 检查记录

临时工具链：Go 1.27.0、golangci-lint 2.13.0；使用 `/tmp/carpool-gopath`、`/tmp/carpool-gocache` 和隔离 Docker 测试数据库。应用依赖清单与锁文件未改。命令从 `backend/` 执行，前端和 lab 从项目根执行。

| 检查 | 结果 | 本地证据 |
| --- | --- | --- |
| `go test -tags=unit -count=1 ./...` | 58 个有测试包通过 | `/tmp/carpool-phase1a-unit-final.log` |
| `CI=true go test -tags=integration -count=1 ./...` | 52 个有测试包通过 | `/tmp/carpool-phase1a-integration-final.log` |
| core 与 CLI：`go test -race -tags=integration -count=1 ./internal/carpool ./cmd/carpool-test-order` | 2 包通过 | `/tmp/carpool-phase1a-core-cli-final.log` |
| HTTP routes：`go test -race -tags=integration -count=1 ./internal/server/routes` | 通过 | `/tmp/carpool-phase1a-http-final.log` |
| `golangci-lint run --timeout=10m ./...` | 0 issues；CORS 修复后再次通过 | `/tmp/carpool-phase1a-lint-post-cors.log` |
| `go build -tags=embed -o /tmp/carpool-phase1a-server ./cmd/server` | CORS 修复后通过 | 本地二进制，命令退出 0 |
| `go build -o /tmp/carpool-phase1a-test-order ./cmd/carpool-test-order` | 通过 | 本地二进制，命令退出 0 |
| `make test-frontend` | lint/typecheck 与关键集 17 文件 / 227 测试通过 | `/tmp/carpool-phase1a-frontend.log` |
| `CARPOOL_BASELINE_BINARY=/tmp/carpool-baseline-server node tools/carpool/lab.mjs` | 16 组 A/B 回归通过 | 本次私有 runtime 的 `results.json`，见下文 |
| CORS 修复后：`go test -race -tags=unit -count=1 ./internal/server/middleware` | 通过 | `/tmp/carpool-phase1a-cors-green.log` |

全量 unit/integration 和 A/B lab 在最后一行 CORS allowlist 修复之前执行；修复后复跑的是完整中间件 unit/race、全后端 lint 和构建，不将此前全量结果称为修复后的全量复跑。最终新增的 full-event 回滚断言由全量 integration 覆盖；core/CLI race 运行在该断言补充之前。

HTTP 正向集成测试使用真实签发/验证的 JWT、原 JWT/admin 中间件及真实 PostgreSQL，但用户仓储为测试夹具，正向管理员业务 harness 单独隔离合规确认。完整路由测试 `TestCarpoolAdminComplianceAndAudit` 另行证明未经确认会返回 423；没有代用户接受真实合规声明。此项不等于 1B 浏览器全流程通过。

前端本轮只复跑关键集；阶段 0 的全量 284 文件 / 2163 测试证据仍见 `PHASE_0_FRONTEND_FIXES.md`，不混作本轮全量前端运行。

A/B 实验结果位于 `/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-gJhTuE/results.json`。16 组覆盖原版缺口复现、SSO、本地主体映射、跨站 JWT/Key 隔离、默认拒绝、有效授权、回调无令牌、撤销/暂停/服务到期/租约过期、多协议入口、在途流和默认 A 兼容性。实验自行停止进程并清理容器；结束后 Docker 列表无本次实验或 testcontainers 容器，其他项目容器未操作。runtime 内有临时凭证，不能提交或公开原始配置/日志；临时日志可能被系统清理。

## 审查与修复

1. 初始 core 测试先因未实现的类型/方法编译失败，见 `/tmp/carpool-phase1a-core-red.log`，再完成实现并转绿。
2. 行锁等待后时间失效：预约/确认在用户行锁等待后重新读取数据库时间与车状态；创建车辆在管理员行锁之后校验招募期限。分别有失败断言日志 `carpool-phase1a-races-red.log`、`carpool-phase1a-create-red.log` 和后续通过记录。
3. 审查补充不同键并发、全 held 非满员、新 service 清理、最后确认对取消、最终事件失败回滚和带存量数据迁移测试。
4. CORS 原 allowlist 遗漏 `Idempotency-Key`，跨域浏览器不能正常预约。新增 OPTIONS 回归测试先失败（`carpool-phase1a-cors-red.log`），仅补充请求头后转绿；非允许来源仍返回 403，无来源策略放宽。

## 后续限制

- `carpool_main.enabled`、`test_orders_enabled` 均默认 false；main/node 模式互斥。测试订单关闭并不关闭所有只读及管理功能，这是 P1-14 的明确细化。
- 清理 worker 启动即扫描，之后每分钟一次，每次最多 100 辆车、30 秒超时，失败下个周期重试；未完成生产负载、积压容量和告警验收。
- 无真实付款、退款、邮件、生产同步、节点激活、实际服务计时、第三节点或真实出口验证。
- 下一步是 1B：管理员车辆页、登录后拼车大厅、“我的车”、禁用/冲突/空态和桌面移动端联调；继续使用本地测试订单。
