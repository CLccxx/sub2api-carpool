# 阶段 3 本地交付与验收

日期：2026-09-19（Asia/Shanghai）

结论：已完成获批的 3A、3B、3C 本地开发范围。用户只登录 A，在 A 管理对应车辆的节点密钥和本人用量；模型客户端直接访问 B/C。这不是生产上线验收，也不包含阶段 4 的车辆正式开通。

## 交付内容

| 子阶段 | 已交付 | 详细记录 |
| --- | --- | --- |
| 3A | 节点登记、加密凭证、HTTPS 白名单及目标校验、连接检查和凭证版本控制 | `PHASE_3A_RESULTS.md` |
| 3B | 独立内部用户、委托密钥、隔离用量、版本授权、限时租约、持久化同步和失败重试 | `PHASE_3B_RESULTS.md` |
| 3C | 管理员节点连接/同步界面、用户按车密钥/用量控制台、敏感数据生命周期、桌面/移动端联调 | `PHASE_3C_RESULTS.md` |

正常入车、满员、连接检查和同步只产生 prepared 映射，不写 active 服务授权，不生成余额或订阅，服务日期仍为空，用户不能创建可用密钥。active/计费数据只由隔离测试夹具提供；生产服务器没有测试开通接口。

## 设计逐项核对

以下对应 `PHASE_3_CONTROL_PLANE_DESIGN.md` 的编号章节，按实际源代码、测试断言和本地输出核对，不把计划中的文件名当作已实现的证据。

| 设计章节 | 实现与验收依据 | 结论 |
| --- | --- | --- |
| 1. 请求边界 | A 邮箱/密码登录的真实浏览器流程；独立 B/C 生产二进制直连本地模型桩，A 只查询原生网关产生的使用记录 | 本地通过；无密码复制、成员 B 会话或 A 模型代理 |
| 2. 复用范围 | 原生 APIKeyService、ent 事务和用户用量管理接口；固定委托命令；生产路由认证、合规和二次验证测试 | 通过；不是任意节点设置编辑器 |
| 3. 连接凭证 | `control_crypto`、`control_http`、`control_store` 及测试；独立 AES-GCM 根、严格 DNS/拨号/TLS、代次和检查操作防旧响应；前端掩码及审计省略凭据 | 通过；全局管理员 Key 未被降权 |
| 4. 内部身份 | `managed_members`、`managed_grants`、`node_access`；并发原子预配、零余额/无默认订阅/无邮件身份、managed 与 OIDC 隔离；实际 B 登录拒绝 | 通过；管理员恢复保留 |
| 5. 委托协议 | `carpool_control` 路由、`control_member_store`/`operations`/`queries`；固定成员归属、日期分页、响应字段白名单、元数据幂等记录、删除及撤销屏障 | 通过；提交后丢响应并刷新重试不重复建 Key |
| 6. 同步恢复 | 010 迁移、`control_jobs`/`control_sync_publish`、worker；单调版本、持久化领取、最多六次尝试、60 秒对账和 15 分钟上限；重启/乱序/撤权/租约与流式测试 | 通过；网络故障不承诺即时撤权 |
| 7. 阶段边界 | `main_service_window` 和 prepared 状态测试；生产代码无开通命令，旧未开通退款接口拒绝已服务记录 | 通过；实际开通仍属于阶段 4 |
| 8. 页面及本机验收 | 13 个连接组件测试、17 个控制台测试、完整前端回归、14 组新浏览器场景、真实模型网关、旧 12/18/16 组回归 | 本地通过；完整后端 race 的独立缺口见下文 |
| 9. 代码落点 | 增量迁移 004-010、carpool/service/handler/routes 边界、既有 Vue 样式及图标；大厅布局未重做；保留现有工作区 | 通过；无提交、推送或生产部署 |

执行计划中的个别文件名按既有边界调整：门禁实现在 `managed_grants.go`，查询客户端在 `control_queries.go`，006 迁移是 `006_managed_key_commands.sql`。浏览器脚本是 `tools/carpool/control-browser.mjs`，配合 `control_browser_integration_test.go`、`control_model_integration_test.go` 和 `control_native_http_integration_test.go`，没有另建重复的 managed-lab 服务。

## 最终检查

| 检查 | 结果 |
| --- | --- |
| 前端全量测试 | 293 文件 / 2311 测试通过 |
| 前端 lint、类型、语言键和生产构建 | 通过；已有 Browserslist/包体积警告保留 |
| 受影响 PostgreSQL/race | carpool 75.824s、routes 14.629s、config 3.459s、test-order CLI 2.396s，通过 |
| 受影响 service/handler/middleware unit/race | 1.483s / 2.738s / 1.050s，通过 |
| 全后端默认标签 lint | 0 issues |
| carpool 集成标签 lint | 最终 0 issues；修正测试格式并注明本地二进制启动的信任边界 |
| 嵌入前端的服务器、两个 CLI、最终宿主测试程序编译 | 通过 |
| 最后一次原生联调 | subject 并发测试 1.51s；浏览器 14 组 21.99s；真实 B/C 模型网关 11.82s；A/B/C HTTP 6.03s，全部通过 |
| 历史人工/测试订单/OIDC 回归 | 12 / 18 / 16 组通过，使用当前服务器/CLI |

最后一次浏览器结果与截图：
`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-browser-1914787774`。
最后一次模型日志：
`/var/folders/kr/2mdsqs0x69n3rn8bdh4800rh0000gn/T/carpool-control-model-2226517076`。
前端全量日志：`/tmp/carpool-phase3c-frontend-tests.log`。历史复跑和工具说明见 `tools/carpool/PHASE_3C.md`，临时路径可能被系统清理，不应公开上传其凭证日志。

桌面 1440px 英文、手机 390px 中文和 320px 英文通过布局断言并检查截图。浏览器阻止外部请求，截图不包含完整 Key。异常节点显示不可用，不把查询错误显示为零用量。最终 `git diff --check` 通过，分支与工作区保留；见执行计划的收尾记录。

## 尚未覆盖

- 更广的 `internal/service -race -tags=unit` 仍失败，涉及未修改的监控、Grok、用量清理和 OpenAI 测试共享状态。已独立复现，普通全量 service 单元测试通过；详见 `PHASE_3B_PROGRESS.md`。不能声称整个后端 race-clean，也没有在干净上游检出上完成这次复现。
- 正向管理实验使用原生测试路由，不自动接受生产合规声明；生产合规拒绝和二次验证分别由路由测试覆盖。新浏览器实验没有配置刷新令牌缓存，其 access-token fallback 不等于刷新会话验收；旧 OIDC 回归覆盖旧刷新/撤销链路。
- 租约失败测试以停止续租并调整隔离数据库时间窗验证，不是 15 分钟真实公网断网实验；生产公网 TLS、独立 IPv4/IPv6 出口、负载容量和备份恢复仍未验收。
- 全局管理员 Key 泄漏仍有节点管理员级风险；加密和 A 的调用白名单不能替代 B 端权限收敛、网络访问限制和恢复演练。
- 未配置 SMTP、在线支付、真实上游账号或正式服务器，未购买资源、接受声明、发布镜像或推送 GitHub。

## 下一阶段

阶段 4 先确定分组/额度或订阅策略，再实现部署模板、节点及成员预检、管理员明确确认开通、共同服务起止时间和授权确认后的可用状态。真实服务器、DNS/TLS、上游凭据和独立出口验收另行授权，不因阶段 3 本地通过而自动执行。

当前只保留临时预览，见 `HANDOFF.md`；车队未开通。保持 `codex/phase0-node-access` 和全部未提交工作，不合并、不清理工作区。
