# 验收记录

验收日期：2026-08-22（Asia/Shanghai）

## 静态与测试

以下命令均以退出码 0 完成：

```bash
cd backend
go test ./...
go test -race ./...
go vet ./...
go build ./...

cd ../frontend
npm run typecheck
npm run build

cd ..
docker compose config --quiet
```

- 路由集成测试覆盖 viewer 写入 403、operator 放行 403、reviewer 放行成功、已决记录更新 409，以及色彩配置/放行决定两条不可变修订链。
- 非测试 Go 代码为 38 个文件、3143 行，符合提示词的 26-38 文件与 2700-3900 行范围。

## 空卷 Compose 与 API

先执行 `docker compose down -v --remove-orphans`，随后以 `KEEP_RUNNING=1 ./scripts/validate.sh` 从空命名卷启动。MySQL、Redis、backend、frontend 均通过 healthcheck。

脚本实际验证：

- `/healthz`、前端首页、session、runtime、overview 和四组实体列表正常。
- 创建设备并推进状态后，审计总数和迁移计数同步增加。
- viewer 对写接口和审计接口均得到 403。
- operator 可采集校样并提交 review，但接收校样得到 403；reviewer 接收成功。
- operator 创建 draft 决定后直接 release 得到 403；reviewer 放行成功并生成 v2。
- 决定详情返回 v2/v1 两条修订，操作者分别为 reviewer/operator，请求 ID 分别为 `release-review-smoke`、`release-create-smoke`。

## 内置 Browser

只使用 Codex 内置 Browser 验收，没有调用外部 Chrome。

| 页面/场景 | 实测结果 |
|---|---|
| `/presses` | 列表、搜索区、新增确认框和 `ready -> setup` 状态推进正常 |
| `/runs` | 三条批次可见，`RunStateBadge` 显示待装版/印刷中/校样中 |
| `/proofs` | `ColorTable` 展示四条读数；详情复用同一组件并显示 v3 已接收校样 |
| `/release` | 放行依据读数、相关批次状态和决定详情正常；详情显示 v2/v1 完整版本链 |
| `/audit` | 审计列表显示操作者、迁移前后状态、实体和请求 ID |
| RBAC | viewer 无新增/推进/审计入口且直达 `/audit` 被重定向；operator 隐藏复核动作；reviewer 显示放行与审计入口 |
| 响应式 | 390x844 视口下导航、指标、工具栏和滚动表格无页面级横向溢出，`documentWidth == viewport == 390` |
| 控制台 | 全流程完成后 error/warning 日志为 0 |

最终交付前执行：

```bash
docker compose down -v --remove-orphans
```

# 分批印刷放行验收记录

验收日期：2026-09-24（Asia/Shanghai）

## 静态与测试

```bash
cd backend
go test ./...
go test -race ./...
go vet ./...
go build ./...

cd ../frontend
npm run typecheck
npm run build
```

以上命令均以退出码 0 完成。新增集成测试 `TestPartialRunReleaseLedger`（`backend/internal/router/router_test.go`），`go test -race -count=8` 连续运行全部通过，覆盖：

- 种子批次 PR-003 计划 6000、台账 2 笔累计 3600、状态保持校样中、剩余 2400，序号区间 1–2000、2001–3600 不重叠。
- viewer/operator 提交放行均为 403；复核员手动 `proofing -> released` 迁移返回 422（满数只能由台账累计触发）。
- 印刷机台不存在返回 422 且带可见中文原因；超计划（400+700>1000）返回 422，失败后台账仍只有 1 笔、累计 400 不变。
- 两人以同一版本同时提交剩余 400 份：恰好一笔 200、一笔 409 `release_conflict`；最终批次 released、累计 1000、剩余 0、台账 2 笔，第二笔区间 401–1000。
- 满数后再提交返回 422（只有校样中的批次才能登记放行）。

## 运行时 API 冒烟（SQLite 模式）

- 建批返回 `plannedCopies=1000 / releasedCopies=0 / remainingCopies=1000`。
- 首批 400 份（机台 PU-001）后状态 proofing、剩余 600；超计划失败带本次/累计/计划/剩余数字且累计不变；过期版本返回 409。
- 并发两笔最终 600 份：200 与 409 各一，失败原因 `同一批次已有放行先一步提交，请刷新后重试`。
- 审计写入 `RunRelease release copies:400 -> copies:1000` 与满数时的 `PrintRun transition proofing -> released`。

## 前端

- `npm run typecheck` 与 `npm run build` 通过；产物包含「分批印刷放行 / 计划份数 / 累计数量 / 剩余数量 / 本次完成数 / 印刷机台」文案。
- `/release` 顶部放行看板逐行显示计划、累计、剩余和进度条，校样中批次可内联登记本次完成数与机台；失败原因在行内显示并自动刷新台账；满数批次移入已放行区显示「已满数放行，共 N 笔记录」。
- `/runs` 列表增加「计划 / 累计 / 剩余」列，校样中批次显示「分批放行」入口，详情弹窗展示互不重叠的放行区间、机台、复核员、请求 ID。

