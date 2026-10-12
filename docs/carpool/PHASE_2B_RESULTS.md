# 阶段 2B 本地验收结果

状态：已完成批准范围的本地实现与自动化验收。仅使用虚构用户及站外收款/退款记录；不是生产上线或支付渠道验收。

## 交付范围

- 管理员车辆页增加人工加人入口：搜索已注册有效账号、明确选择邮箱和用户编号、固定单席金额、支付宝/微信渠道、实际收款时间、可选参考号和内部备注。先确认自行核实到账，再核对并提交。
- 人工订单页支持车辆、订单状态、退款状态筛选及分页。可查看私有登记信息、有原因地移除成员、处理已取消车辆的待退款并登记已完成的站外全额退款。
- 招募中人工车辆按现有站点联系方式提供纯文本联系入口；缺少联系方式不生成虚假按钮，管理员侧提示配置。保留已确认的 3/2/1 列紧凑大厅。
- 「我的车」展示本人席位、人工收款、待部署、待退款/已人工退款及历史记录。人工记录不展示自助占位、取消、支付或倒计时，即使关闭新增也不会退回测试订单交互。
- 满员仍只进入待部署，服务起止为空；满员后移除成员转待处理，不自动补位，不改变原交付承诺，不授予节点权限。
- 请求结果未知时锁定原操作者、目标、请求正文和幂等键；刷新后可原请求重试或人工核对。管理员数据不出现在用户接口/页面。

主要文件：`frontend/src/components/carpool/CarpoolEnrollment.vue`、`CarpoolManualOrders.vue`、`CarpoolPayment.vue`、`frontend/src/composables/useCarpoolManualCommand.ts`、`frontend/src/utils/carpoolManualJournal.ts`，以及既有管理员车辆/大厅/我的车/API/双语文件。后端生产收款事务沿用 2A，本轮扩充浏览器 harness 与 lab 参数。

## 实测结果

| 检查 | 最终结果 |
| --- | --- |
| 前端全量 `pnpm test:run` | 290 文件、2263 项通过 |
| 前端 `pnpm lint:check` | 退出码 0 |
| `pnpm build` | 双语键检查、`vue-tsc -b`、Vite 构建通过 |
| 人工浏览器 | 12 组通过，桌面英文、手机中文、320px 长文本，页面异常为零 |
| 原测试订单/大厅浏览器 | 18 组通过，含原有响应丢失、预约到期、取消、3/2/1 列、暗色与权限开关 |
| A/B 认证授权 lab | 16 组通过，使用独立原版/最终修改版嵌入服务 |
| 受影响后端 `-race -tags=unit,integration` | 指定 carpool/manual/main/test-order/node-access 测试通过，具体范围见下文 |
| 后端嵌入资源与本机 CLI | server/test-order/membership 编译通过；最终前端后重新构建 server 再跑 A/B |
| 后端全量普通 lint | golangci-lint 2.13.0，0 issues |
| 集成标签路由 lint | `--build-tags=integration ./internal/server/routes/...`，0 issues |
| 本机交互预览 | 管理员和用户入口分别通过 Chrome 渲染检查，无页面异常 |
| 最终差异/脚本语法 | `git diff --check` 与两份浏览器/lab 脚本的 `node --check` 通过 |

后端本轮采用 Go 1.27.0 官方容器，实际选择命令：

```sh
go test -p 1 -mod=readonly -race -tags=unit,integration -count=1 \
  -run 'Test(Carpool|Manual|Main|TestOrder|NodeAccess)' \
  ./internal/carpool ./internal/config ./internal/handler ./internal/service \
  ./internal/server/middleware ./internal/server/routes \
  ./cmd/carpool-test-order ./cmd/carpool-membership
```

结果为 carpool 31.619s、config 1.093s、handler 1.076s、service 1.173s、middleware 1.045s、routes 3.931s、test-order 2.399s；membership CLI 无测试文件。这是明确筛选的受影响测试，不是全后端所有包全量测试。浏览器是随后单独显式启用的运行，未把普通集成命令中的跳过算成通过。

构建保留已有 Browserslist 数据陈旧、较大 chunk 及静态/动态导入提示；没有为消除提示修改依赖或无关打包架构。完整复跑和预览说明见 `tools/carpool/PHASE_2B.md`。

## 边界与修正证据

| 风险 | 检查方式 |
| --- | --- |
| 误选用户、搜索结果乱序 | 改搜索文字立即清除选择，旧响应不得覆盖；Vitest 覆盖 |
| 金额/时间边界 | 固定整数分金额，拒绝未来时间及早于收款的退款时间；API/helper/界面与后端校验 |
| 已提交但响应丢失 | 浏览器先确认服务端提交成功，再丢弃返回；等待可重试提示，刷新并原键重试，数据库仅一笔订单 |
| 旧响应覆盖新恢复记录 | 先复现失败，再增加幂等键比较保护；旧响应不能删除新请求 |
| 登出后的内部备注残留 | 先复现退出登录清理失败，再将清理入口接入 auth store；即使页面已卸载也生效 |
| 接口强制登出分支遗漏 | 两个失败测试复现无刷新令牌/刷新被拒绝后的残留；接入同一清理函数，临时网络/5xx/429 不清理 |
| 退出后迟到响应重建记录 | composable 回归验证清空后的 journal 不会被旧失败响应恢复 |
| 私有凭据泄漏/联系方式 HTML | 用户端无内部备注等管理员字段；浏览器验证 `<b>contact</b>` 为纯文本 |
| 取消不等于退款完成 | 退出生成 pending，另行确认后才 refunded；关闭新增仍可操作，取消车辆仍在退款队列 |
| 满员不等于开通 | 手工浏览器检查无节点授权和余额变更，服务时间为空；真实事务验证另见 2A |
| 跨站与缓存权限退化 | A/B 检查独立 JWT/API Key、旧会话/Key 拒绝、缓存预热撤销、多协议入口及租约失效 |

截图检查覆盖桌面满员、人工登记、异常重试、手机登记/退款/我的车及窄屏长文本。浏览器自动断言页面与关键表单控件不横向溢出。异常重试截图最初过早抓到处理中，已收紧为等待「结果未知」提示及可用重试按钮后再截图，最终 12 组重跑通过。

恢复正文仅放本标签页 `sessionStorage`（含可选参考号和备注），按管理员编号及动作范围隔离，另有内存回退，不写入 `localStorage`。主动退出/确定会话失效会清除，临时网络失败保留。关闭标签页、清理浏览器数据或存储禁用后刷新可能失去本地恢复信息，必须用服务器订单和站外流水核对，不能直接重复添加。它不是外部流水总账。

## 本地证据

- 最终人工浏览器：`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-2204756720`，含 `results.json` 和截图。
- 最终旧流程浏览器：`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-browser-2080684587`。
- 最终 A/B lab：`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-lab-sSyPh2`，run ID `carpool-p0-a49e11df`，结果 passed。
- 前端最终日志：`/tmp/carpool-2b-final-tests.log`、`/tmp/carpool-2b-final-build.log`。
- 基线源树 `/tmp/carpool-2b-baseline.4oS8jb` 来自 `881f3202694c6bc932446931a30c27d9675178b9`，原版和修改版二进制分别为 `/tmp/carpool-2b-bin/baseline-server`、`manual-server`；未拿修改版当原版。

本轮保留的八小时本机预览：

```text
管理员 http://127.0.0.1:52233/admin/carpool?preview_user=1
用户   http://127.0.0.1:52233/carpool?preview_user=2
PID    18548
日志   /tmp/carpool-2b-preview-DbKDF7/preview.log
```

入口只切换虚构测试身份，URL 不包含令牌。同一浏览器配置文件共享站点身份，需同时对照时使用不同配置文件。预览含 2/3/4 座车辆，其中 2 座车已满员；可搜索 `user1@example.test`、`user2@example.test` 在其他车体验人工入车。端口仅回环绑定，八小时自动停止；提前结束前先确认 PID 仍对应此测试 runner，再发送 SIGINT。临时目录可能包含实验凭据，不应提交或公开上传。

## 未交付范围

- 没有真实支付、退款执行、SMTP、商户申请、采购、生产部署或远程 CI。正式开放前仍需独立上线评审。
- 正向页面 harness 使用真实 JWT 与 PostgreSQL，但用户搜索及无关页面依赖是受认证的夹具；它省略合规中间件且没有接受声明。生产权限/423 拦截由独立路由测试验证，未修改生产合规逻辑。
- A/B 使用现有人工授权 CLI，不是主站自动同步成员的端到端交付。没有 C 节点、公网出口、生产 TLS、故障恢复或容量基准。主站人工入车不会让用户直接使用 B。
- 所有既有阶段 0/1/2A 与本次 2B 改动保留在当前工作区，没有提交、合并、推送或重置。后续进入阶段 3 的节点授权同步与阶段 4 开通交付；在线支付继续延期。
