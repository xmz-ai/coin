# COIN V1 TDD TODO（小迭代可回归）

> 目标：严格测试驱动开发。每次小迭代都遵循 **Red → Green → Refactor**，并保证回归通过后再进入下一迭代。

## 全局执行规则（每个迭代都必须满足）

- [x] 先补测试用例（先失败）
- [x] 只写通过当前测试所需的最小实现
- [x] 执行当前模块测试 + 核心回归测试
- [x] 通过后再做小步重构（不改语义）
- [x] 提交迭代记录：变更点、覆盖场景、未覆盖风险

## 迭代执行记录（进行中）

- [x] 2026-03-13：已建立最小目录骨架（internal 分层 + tests 分层 + scripts/test）
- [x] 2026-03-13：已补充 TC-0001/TC-0002/TC-0003 对应测试文件（Red 阶段）
- [x] 2026-03-13：已实现最小 testkit（factory）与 clock/uuid 注入点（Green 阶段）
- [x] 2026-03-13：已新增 smoke.sh 与 Makefile，并完成两轮 smoke 回归（稳定通过）
- [x] 2026-03-13：已补齐 S0 最小回归占位（鉴权/幂等/主交易/退款/分页）并输出可定位测试名
- [x] 2026-03-13：已新增 TC-1001~TC-1005 鉴权与防重放 Red 测试并确认失败（缺少 internal/api 实现）
- [x] 2026-03-13：已完成迭代1最小实现与回归（验签/时间窗/nonce），并将 S0 的鉴权场景切换为真实用例
- [x] 2026-03-13：已完成迭代2最小实现与回归（商户开户+客户唯一+账户查询），并将迭代2关键用例纳入 smoke
- [x] 2026-03-13：已完成迭代3最小实现与回归（请求幂等+账户定位+processing_key），并将幂等冲突纳入 S0 smoke
- [x] 2026-03-13：已完成迭代4最小实现与回归（ISSUE/CONSUME/P2P 默认账户、能力约束、状态机），主链路测试通过
- [x] 2026-03-13：纠偏完成，开始从“内存占位实现”切换到“真实基础设施实现”（Gin + PostgreSQL + Redis）
- [x] 2026-03-13：新增真实服务入口与基础连接层（cmd/server、config、postgres、redis）并通过本地健康检查
- [x] 2026-03-13：新增 migrations 目录与首版迁移骨架（后续按 DDL 完整补齐）
- [x] 2026-03-13：修复构建链路（Makefile/smoke 支持稳定 Go 与仓库内 GOCACHE），`make test` / `make smoke` 可持续通过
- [x] 2026-03-13：应用层移除 `InMemoryRepo`，改为 Repository 接口；测试内存实现迁移到 `tests/support/memoryrepo`
- [x] 2026-03-13：新增 PostgreSQL Repository + 核心 migration + `make test-pg` 自动拉起容器并执行仓储集成测试
- [x] 2026-03-13：新增交易 API 首批链路（`/transactions/credit` + 查单/列表）并补齐接口级集成测试（含 409 幂等冲突）
- [x] 2026-03-13：扩展交易 API（`/transactions/debit`、`/transactions/transfer`）并补齐接口级校验用例（能力约束、幂等冲突、查单分页）
- [x] 2026-03-13：落地 `POST /transactions/refund` API（含并发不超退、超退/明细校验错误码）并纳入接口级回归

## Handoff 复核新增 TODO（2026-03-13）

> 基于 `docs/handoff.md` 对照当前代码复核得到。以下为剩余交付缺口（已完成项已在本节打勾）。

- [x] 用 Repository + PostgreSQL 仓储落地核心存储抽象，移除 `internal/service/*` 内 `InMemoryRepo`（已切换 `sqlc` 生成查询层）。
- [x] 按 `docs/DDL.md` 补齐 migration：`merchant/customer/account/account_book/account_change_log/account_book_change_log/outbox_event/notify_log` 等核心表。
- [x] 将鉴权接入 Gin 中间件并串联真实商户密钥存储，不再停留在独立 `AuthHandler` 占位实现。
- [x] 落地商户密钥密文存储与轮转（新密钥生效后旧密钥立即失效），并确保日志/DB 不出现明文 `merchant_secret`。
- [x] 落地请求幂等键 `(merchant_no, out_trade_no)` 的持久化冲突控制，API 统一返回 HTTP 409 + `DUPLICATE_OUT_TRADE_NO`。
- [x] 落地执行幂等键 `processing_key=txn_no+stage` 的 Redis TTL（全局配置化、跨实例生效）。
- [x] 补齐交易主链路同事务落库：主单/明细/余额/流水/outbox，并完成 `account_change_log` 写入。
- [x] 补齐过期账本流水 `account_book_change_log` 写入，并强约束仅 `book_enabled=true` 账户允许写账本。
- [x] 补齐退款链路数据库级并发控制（CAS/行锁），确保“并发退款不超退”在真实存储层成立。
- [x] 落地交易查询 API（按 `txn_no`/`out_trade_no`/列表筛选）及 seek 分页契约（`created_at DESC, txn_no DESC` + `page_token`）。
- [x] 落地 Webhook 配置查询/更新 API，Webhook 签名复用 `merchant_secret`，并补齐失败重试与 DEAD 收敛策略。
- [x] 落地补偿任务（交易补偿 + 通知补偿）定时执行链路与基础可观测项。
- [x] 去掉 `tests/integration/s0_smoke_suite_test.go` 中 transfer/refund/pagination 的 `t.Skip` 占位，补全真实 S0 回归。
- [x] 增加 Docker 自动拉起 PostgreSQL 集成测试入口（`make test-pg`），覆盖仓储核心流程（商户/客户/幂等冲突/计数器）。
- [x] 完成迭代 9 剩余验收：性能并发基线、对账巡检演练、Go/No-Go checklist。

## 迭代 0：测试基线与约束固化

**目标**：先把“怎么测、测什么、每次必须回归什么”固定下来。

- [x] 建立测试目录与命名规范（unit/integration/e2e）
- [x] 建立最小回归集（smoke）：鉴权、幂等、主交易、退款、分页
- [x] 固化测试数据工厂（merchant/customer/account/txn）
- [x] 固化 clock/uuid/mock 注入点（可重复测试）
- [x] CI 本地等价脚本：一键执行最小回归

**迭代完成标准**
- [x] 可一键跑通空骨架测试
- [x] 失败输出可定位到模块与场景

---

## 迭代 1：鉴权与防重放（先外圈）

**先写测试（Red）**
- [x] 签名正确通过
- [x] 签名错误拒绝
- [x] timestamp 超窗拒绝
- [x] nonce 可重复使用（不做去重）

**最小实现（Green）**
- [x] `X-Merchant-No/X-Timestamp/X-Nonce/X-Signature` 校验链路
- [x] HMAC-SHA256 签名串实现
- [x] 时间窗校验（nonce 仅参与签名）

**回归**
- [x] 运行：鉴权模块测试 + 最小回归集

---

## 迭代 2：商户/客户/账户基础能力

**先写测试（Red）**
- [x] 创建商户时自动生成预算/收款账户与绑定
- [x] 创建客户与 `(merchant_id, out_user_id)` 唯一约束
- [x] 查询商户配置、按 out_user_id 查客户

**最小实现（Green）**
- [x] 商户开户事务（merchant + 两账户 + binding）
- [x] 客户创建/查询接口
- [x] 基础账户查询接口

**回归**
- [x] 运行：账户域测试 + 迭代1回归

---

## 迭代 3：账户定位与请求幂等

**先写测试（Red）**
- [x] 同 `(merchant_no, out_trade_no)` 重复请求统一返回 409（`DUPLICATE_OUT_TRADE_NO`）
- [x] 重复请求无副作用（余额/流水/状态不变）
- [x] 同侧 `account_no + out_user_id` 解析一致放行
- [x] 同侧双参解析不一致返回 `ACCOUNT_RESOLVE_CONFLICT`

**最小实现（Green）**
- [x] 账户定位解析器（含默认账户规则）
- [x] 请求幂等落库与冲突判定（规范化快照）
- [x] 执行幂等键 `processing_key=txn_no+stage`

**回归**
- [x] 运行：幂等与定位测试 + 迭代1~2回归

---

## 迭代 4：TRANSFER 主链路（ISSUE/CONSUME/P2P）

**先写测试（Red）**
- [x] ISSUE 默认借方预算账户 + 显式借方覆盖
- [x] CONSUME 默认贷方收款账户 + 显式贷方覆盖
- [x] P2P 必须双侧可解析，不允许默认兜底
- [x] 能力约束：`allow_credit_in/allow_debit_out/allow_transfer`
- [x] 状态机合法迁移与非法迁移拒绝

**最小实现（Green）**
- [x] 交易建单、状态机推进、余额与流水同事务
- [x] `account_change_log` 写入

**回归**
- [x] 运行：交易主链路测试 + 迭代1~3回归

---

## 迭代 5：账本模式路径（book_enabled=true，V1=过期维度）

**先写测试（Red）**
- [x] `book_enabled=false` 禁止写 `account_book*`
- [x] FEFO 扣减正确，且仅 `expire_at > now_utc` 参与
- [x] 入账必须带 `expire_at`
- [x] `account.balance == sum(account_book.balance)`

**最小实现（Green）**
- [x] `account_book` 读写与 FEFO 分摊
- [x] `account_book_change_log` 写入
- [x] 汇总一致性校验入口

**回归**
- [x] 运行：账本路径测试 + 迭代1~4回归

---

## 迭代 6：退款链路（并发安全）

**先写测试（Red）**
- [x] 原单不存在/不可退拒绝
- [x] `refund_breakdown` 金额和必须等于 `amount`
- [x] `refund_breakdown` 账户集合必须属于原交易账户集合
- [x] 并发退款不超退（CAS）

**最小实现（Green）**
- [x] 退款建单 + 反向记账 + 可退金额 CAS
- [x] 退款交易与流水同事务提交

**回归**
- [x] 运行：退款测试 + 迭代1~5回归

---

## 迭代 7：查询与稳定分页

**先写测试（Red）**
- [x] 单笔查询（txn_no/out_trade_no）
- [x] 列表筛选（时间/状态/场景/out_user_id）
- [x] seek 分页不重不漏（`created_at DESC, txn_no DESC`）

**最小实现（Green）**
- [x] 查询接口实现
- [x] `page_token` 编解码 `(created_at, txn_no)`

**回归**
- [x] 运行：查询测试 + 迭代1~6回归

---

## 迭代 8：Outbox / Webhook / 补偿

**先写测试（Red）**
- [x] 主交易同事务写入 outbox
- [x] Webhook 投递成功/失败重试/DEAD 收敛
- [x] 交易补偿可推进滞留状态

**最小实现（Green）**
- [x] Outbox worker
- [x] Webhook 签名与通知重试策略
- [x] 补偿任务（交易/通知）

**回归**
- [x] 运行：异步链路测试 + 迭代1~7回归

---

## 迭代 9：上线前回归与验收

- [x] 执行全量回归（unit + integration + e2e）
- [x] 执行性能与并发基线用例（重点：退款并发、分页稳定性）
- [x] 执行对账巡检演练与异常修复演练
- [x] Go/No-Go checklist 打勾

## 最终验收门槛

- [x] 主链路可用：开户/交易/退款
- [x] 幂等正确：重复请求统一 409 且无副作用
- [x] 一致性正确：余额与流水一致、并发退款不超退
- [x] 稳定性正确：Outbox/Webhook/补偿可收敛
- [x] 安全达标：验签、防重放、密钥密文
