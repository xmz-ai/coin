# COIN V1 实施交接（精简执行版）

## 最新交接（2026-03-13｜编码发号生产化）

### A. 当前状态（已完成）
- 已有 `merchant_no/customer_no/account_no` 编码规则实现（位数 + Luhn）。
- 商户/客户创建支持系统发号；测试已改为默认走系统发号。
- DDL 已补长度与格式 CHECK，`customer_no` 已入库并打通 sqlc/repository。
- 当前全量测试通过：`go test ./...`。

### B. 当前问题（必须解决）
- 发号源仍是进程内随机序列（重启后可能撞历史号，再靠唯一键/重试兜底）。
- `account` 的创建 SQL 仍是 `ON CONFLICT (account_no) DO UPDATE`，存在撞号覆盖风险，不满足生产要求。

### C. 已确认决策（与产品/Owner对齐）
1. 发号主方案：数据库中心化（DB allocator），不是本地随机。
2. `scope_key` 必须保留并用于分桶并发：
   - `merchant_no/customer_no`：`GLOBAL`
   - `account_no`：`BIN+MMMM+TT`
3. 性能策略：预取号段后内存发号（Hi/Lo）：
   - 批次固定 `100`
   - 低水位 `20` 触发异步续租
4. 冲突语义：冲突即失败，不允许覆盖，不依赖冲突后业务重试。
5. `merchant_no` 仅系统生成（生产路径不允许外部传入）。
6. 序列语义允许跳号（不要求连续）。
7. 校验层级：应用层校验 + DB CHECK 兜底（Luhn 在应用层）。
8. 迁移策略：允许破坏式收敛（项目未上线，不做兼容）。

### D. 下一步实现清单（按顺序）
1. 新增 `code_sequence` 表（主键 `code_type+scope_key`，含 `next_value`）。
2. sqlc 增加序列查询：
   - 初始化序列行（`insert ... on conflict do nothing`）
   - 号段租约（原子 `update ... returning start,end`）
3. 实现 DB-backed allocator（Hi/Lo）：
   - 内存段缓存 + 原子发号
   - 批量租段 `100`，低水位 `20` 异步续租
   - 续租失败处理：当前段可继续发，耗尽则返回 `CODE_ALLOCATOR_UNAVAILABLE`
4. 商户/客户创建链路切到 DB allocator，生产路径移除手工 `merchant_no` 输入。
5. `account_no` 创建改为严格 insert（去掉 upsert 覆盖语义），冲突返回明确错误码。
6. 补集成测试：
   - 并发唯一性（多协程/多实例）
   - 停机重启后继续发号不重复
   - `account_no` 冲突不会覆盖历史账户

### E. 验收标准（这次交付必须满足）
- 停机重启后继续发号，不重复。
- 并发压测下不重复，且吞吐明显优于“每次发号都打 DB”。
- 任意冲突不覆盖历史数据，直接失败。
- `go test ./...` 通过；Postgres 集成用例通过。

> 面向单个资深全栈工程师：只保留“必须做、必须守、必须验收”的信息。

---

## 1) 你要交付什么（范围）

必须交付：
- 商户/客户/账户/账本模型
- 开户、发放、扣减、转账、退款
- 幂等控制、状态机、并发安全
- Outbox + Webhook + 补偿任务
- 接入方查询与配置能力（商户配置、按 `out_user_id` 查客户、交易查询、Webhook 配置）

不做：
- 冻结/解冻
- 跨币种汇兑

---

## 2) 文档优先级（唯一口径）

实现时按以下顺序看文档：
1. `docs/detailed-design.md`
2. `docs/API.md`
3. `docs/DDL.md`
4. `docs/CODE_RULES.md`
5. `test_cases.md`
6. `todo.md`

有冲突：先修文档再改代码（`[Spec Change]`）。

---

## 3) 冻结规则（不可改）

1. 请求幂等键：`(merchant_no, out_trade_no)`
2. 执行幂等键：`processing_key=txn_no+stage`，TTL 全局配置化
3. 同幂等键重复请求：一律拒绝并返回 HTTP 409（不支持重复下单）
4. 账户定位：`out_user_id` 仅用于客户账户定位；同侧双参一致放行，不一致返回 `ACCOUNT_RESOLVE_CONFLICT`
5. ISSUE 默认借方：`budget_account_no`（可显式覆盖）
6. CONSUME 默认贷方：`receivable_account_no`（可显式覆盖）
7. 仅 `book_enabled=true` 账户使用 `account_book`（父账户+子账本模式；V1 仅过期维度）
8. 账本扣减顺序 FEFO；有效边界 `expire_at > now_utc`
9. 账本流水表名固定：`account_book_change_log`
10. 退款 `refund_breakdown`（若传）强约束：
   - `sum(refund_breakdown.amount) == amount`
   - `account_no` 必须属于原交易涉及账户集合
11. 列表分页固定 seek：`created_at DESC, txn_no DESC`，`page_token=(created_at, txn_no)`
12. Webhook 签名复用 `merchant_secret`
13. 密钥轮转：新密钥生效后旧密钥立即失效
14. 主键与外码：内部 UUIDv7，外部数字码（见 `docs/CODE_RULES.md`）

---

## 4) 技术选型（冻结）

后端：
- 语言：Go（稳定版本）
- Web 框架：Gin
- 数据库：PostgreSQL 16
- 缓存/分布式键：Redis 7

数据访问与迁移：
- SQL 访问：`pgx + sqlc`
- Migration：`golang-migrate`

异步与任务：
- Outbox + Worker 同仓实现（单人维护优先）
- 定时任务：`robfig/cron`

可观测：
- 日志：`zap`
- 指标/追踪：OpenTelemetry + Prometheus

测试框架：
- 单元/集成测试主框架：Go `testing`
- 断言与 mock：`testify`（assert/require/mock）
- 集成测试依赖：`testcontainers-go`（PostgreSQL/Redis）
- HTTP 接口测试：`httptest` + Gin router

---

## 5) 开发方式（强制 TDD）

执行来源：
- 任务节奏：`todo.md`（迭代 0→9）
- 测试清单：`test_cases.md`

每个迭代必须按：
1. Red：先写测试并确认失败
2. Green：最小实现使测试通过
3. Refactor：在全绿前提下小步重构
4. 回归：跑 S1（当前模块）+ S0（全局冒烟）

---

## 6) 代码红线（CR 必卡）

1. 不允许绕过幂等
2. 不允许事务外更新余额
3. 不允许 `book_enabled=false` 账户写入 `account_book*`
4. 不允许绕过状态机直接改终态
5. 不允许明文存储/打印 `merchant_secret`

---

## 7) 迭代完成门禁（DoD）

每个迭代必须同时满足：
- 当前迭代新增 TC 全部通过
- S0 全部通过
- 高风险用例全部通过（幂等冲突、并发退款、seek 分页）
- 无新增 P0/P1 缺陷遗留

---

## 8) 最终上线门槛（Go/No-Go）

1. 主链路可用：开户/交易/退款
2. 幂等正确：重复请求统一 409，且不产生任何副作用
3. 一致性正确：余额=流水、并发退款不超退
4. 安全达标：验签、防重放、密钥密文
5. 稳定性达标：Outbox/重试/补偿可收敛

---

## 9) 每日同步模板（直接复制）

- 今日完成迭代：
- 新增/修改 TC：
- 回归结果：S0（通过/失败），S1（通过/失败）
- 风险与阻塞：
- 明日计划：
