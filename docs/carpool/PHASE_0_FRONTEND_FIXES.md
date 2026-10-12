# 阶段 0 收尾：前端基线测试修复

日期：2026-09-16（Asia/Shanghai）

## 范围与结论

两项原版前端基线失败已修复。此次仅修改两个测试文件，没有修改 Vue 业务组件、供应商配置、运行模式、依赖或锁文件。阶段 0 的本地基线与 A/B 验证已具备继续设计阶段 1 的条件；远程 CI 和生产验证仍未执行，不能把它们标为通过。

原版失败历史保留在 `PHASE_0_BASELINE.md`，上轮认证结果保留在 `PHASE_0_AUTH_RESULTS.md`。

## 根因与修复

| 文件 | 根因 | 修复 |
| --- | --- | --- |
| `frontend/src/views/admin/__tests__/ChannelMonitorView.grok.spec.ts` | 组件与共享常量都有 10 个供应商，测试仍写死 8 个 | 将实际按钮的完整 `data-testid` 集合与共享 `PROVIDERS` 对应集合排序比较，仍会检测缺项、多项与重复项；保留 Grok 图标、默认值及用户输入保护断言 |
| `frontend/src/views/admin/__tests__/GroupsView.codexManifest.spec.ts` | 组件读取 `useAuthStore().isSimpleMode`，该隔离测试未提供 auth store | 沿用相邻分组测试的 auth store mock，明确 standard 模式；同时将过期的 `getModelsListCandidates` mock 更正为组件实际调用的 `getModelAllowlistCandidates`，新增参数断言，避免仅解决 Pinia 后留下被组件捕获的接口错误 |

应用入口本身已经安装 Pinia；本次修复不是向运行时补装 Pinia，也不是减少供应商来满足旧断言。Codex manifest 连续两次子组件更新的原始断言保留。

## 红绿验证

先运行下面命令：2 个文件失败，3 项测试中 2 失败、1 通过。修复后同命令退出 0，3 项全部通过。

```sh
pnpm --dir frontend run test:run src/views/admin/__tests__/ChannelMonitorView.grok.spec.ts src/views/admin/__tests__/GroupsView.codexManifest.spec.ts
```

| 最终检查 | 结果 | 本地日志 |
| --- | --- | --- |
| 两个目标文件 | 2 文件 / 3 测试通过 | `/tmp/carpool-phase0-frontend-green.log` |
| `pnpm --dir frontend run test:run` | 284 文件 / 2163 测试通过，退出 0 | `/tmp/carpool-phase0-frontend-full.log` |
| `make test-frontend` | lint、类型检查及 17 文件 / 227 关键测试通过，退出 0 | `/tmp/carpool-phase0-frontend-critical.log` |
| `pnpm --dir frontend run build` | i18n、vue-tsc、Vite 构建通过，退出 0 | `/tmp/carpool-phase0-frontend-build.log` |

失败日志：`/tmp/carpool-phase0-frontend-red.log`。日志位于临时目录，清理后需重新运行。构建产物位于已忽略的 `backend/internal/web/dist/`。

上游测试中的 Vue 组件解析警告、Browserslist 数据陈旧提示和 Vite 大包提示仍存在，本次没有将它们屏蔽或通过升级依赖消除。测试退出成功不等于日志完全无警告。

此次未修改后端，没有重跑后端全量或 A/B 实验；它们的最近结果仍是 `PHASE_0_AUTH_RESULTS.md` 中记录的 57 包 unit、51 包 integration 和 16 组实验通过。本轮未提交、未推送，未接真实支付或部署生产。
