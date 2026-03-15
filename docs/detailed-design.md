# COIN 详细设计（Detailed Design）

> 本文定义“如何实现”，覆盖模块、时序、状态机、数据映射与关键并发控制。
> 口径统一：账本流水表命名使用 `account_book_change_log`。

---

## 参考文档

- `docs/DDL.md`
- `docs/API.md`
- `docs/CODE_RULES.md`

---

## 1. 分层与模块职责

## 1.1 模块分层

1. `api`
   - HTTP 路由、参数校验、签名验签、错误映射、request_id 注入。
2. `service`
   - 用例编排：幂等校验、建单、调用领域服务、提交事务、回包。
3. `domain`
   - 聚合/实体/值对象与不变量：Account、AccountBook、Txn、StateMachine。
4. `db`
   - 数据访问实现：Repo、PostgreSQL 连接、密钥持久化。
5. `platform`
   - 基础工具能力：时钟、ID 生成器、跨层通用组件。
6. `worker`
   - Outbox 投递、通知重试、交易补偿、对账巡检。

## 1.2 建议目录

```text
/internal
  /api
  /service
  /domain
  /db
  /config
  /platform
  /worker
/migrations
```

---

## 2. 核心数据映射（表结构视角）

## 2.1 交易主链路数据

- 主单：`txn`
- 明细：V1 不落 `txn_detail`
- 账户余额：`account`
- 过期账本：`account_book`（条件启用）
- 账户流水：`account_change_log`
- 账本流水：`account_book_change_log`（条件启用）
- 事件：`outbox_event`
- 通知：`notify_log`

## 2.2 关键字段映射

| 业务概念 | 数据字段 |
|---|---|
| 租户隔离 | `txn.merchant_no` |
| 请求幂等 | `txn(merchant_no, out_trade_no)` 唯一键 |
| 交易类型 | `txn.biz_type`, `txn.transfer_scene` |
| 交易状态 | `txn.status` |
| 退款关联 | `txn.refund_of_txn_no`, `txn.refundable_amount` |
| 账户能力 | `account.allow_*`, `account.book_enabled` |
| 过期切分 | `account_book(account_no, expire_at)` 唯一键 |

## 2.3 编码规则落点

- UUIDv7：`merchant_id/customer_id/txn_no/event_id/book_no`
- 数字码：`merchant_no/customer_no/account_no`
- 幂等不依赖编码发号，依赖唯一键：`(merchant_no, out_trade_no)`

---

## 3. 状态机设计

## 3.1 状态集合

- `INIT`
- `PROCESSING`
- `PAY_SUCCESS`
- `RECV_SUCCESS`（终态）
- `FAILED`（终态）

## 3.2 合法迁移

- `INIT -> PROCESSING`
- `PROCESSING -> PAY_SUCCESS | FAILED`
- `PAY_SUCCESS -> RECV_SUCCESS | FAILED`

任何越级更新均返回 `TXN_STATUS_INVALID`，并记录审计日志。

---

## 4. 关键时序设计

## 4.1 交易成功时序（以 ISSUE 为例）

```text
Client
  -> API: POST /transactions/credit
API
  -> Auth: verify signature + timestamp + nonce
  -> Service: executeIssue(cmd)
Service
  -> IdempotencyRepo: check (merchant_no, out_trade_no)
  -> TxnRepo: insert txn(INIT)
  -> TxnRepo: transit INIT->PROCESSING
  -> AccountingDomainService: apply debit/credit
AccountingDomainService
  -> AccountRepo/BookRepo: lock and mutate balance
  -> LogRepo: write account_change_log / account_book_change_log
Service
  -> TxnRepo: transit PROCESSING->PAY_SUCCESS->RECV_SUCCESS
  -> OutboxRepo: insert TxnSucceeded
  -> commit transaction
Worker
  -> OutboxRepo: fetch pending
  -> Webhook: send callback
  -> NotifyRepo: write notify_log & retry schedule
```

## 4.2 扣减时序（CONSUME）

```text
ConsumeRequest
  -> create transfer txn
  -> resolve debit account:
     - debit_account_no > debit_out_user_id (required one)
  -> resolve credit account:
     - credit_account_no > credit_out_user_id > merchant receivable_account_no(default)
  -> validate debit/credit capabilities
  -> apply accounting (debit + credit in one transaction)
  -> write logs + outbox
  -> commit
```

## 4.3 退款时序（并发安全）

```text
RefundRequest
  -> create refund txn
  -> validate refund_breakdown (optional):
     - sum(refund_breakdown.amount) == amount
     - account_no set must be subset of origin txn involved accounts
  -> CAS update origin txn.refundable_amount
     - fail: return REFUND_AMOUNT_EXCEEDED or retryable conflict
  -> reverse accounting path
  -> write logs + outbox
  -> commit
```

---

## 5. 账务路径分流设计

## 5.1 非账本模式账户路径（`book_enabled=false`）

1. 仅更新 `account.balance`
2. 仅写 `account_change_log`
3. V1 无 `txn_detail`，book 路径只落流水表

## 5.2 账本模式账户路径（`book_enabled=true`，V1=过期维度）

### 扣减
1. 查询有效账本：`expire_at > now_utc`
2. 按 FEFO 排序
3. 按需多账本拆分扣减
4. 同步更新 `account.balance`
5. 写 `account_book_change_log` 与 `account_change_log`

### 入账
1. 请求中必须携带 `expire_at`
2. 定位/创建 `(account_no, expire_at)` 账本
3. 更新 `account_book.balance`
4. 同步更新 `account.balance`
5. 写双流水

---

## 6. 幂等、并发与事务边界

## 6.1 双层幂等

1. 请求幂等（数据库）：`UNIQUE (merchant_no, out_trade_no)`
2. 重复请求策略（V1）：
   - 同 `(merchant_no, out_trade_no)` 的后续请求一律拒绝并返回 HTTP 409（不支持重复下单）
   - 拒绝时不得产生任何副作用（余额/流水/状态均不变）
3. 执行幂等（Redis）：`processing_key=txn_no+stage`
   - TTL：全局统一、配置化（本版不固定值）

## 6.2 并发控制

1. 账户/账本更新使用 `SELECT ... FOR UPDATE` 或条件更新。
2. 扣减遵守能力约束与余额条件。
3. 退款通过 `refundable_amount` CAS 递减。

## 6.3 事务边界

单笔交易内以下对象必须同事务：
- `txn`
- `account` / `account_book`（条件）
- `account_change_log` / `account_book_change_log`（条件）
- `outbox_event`

Webhook 投递在异步事务中执行，不阻塞主交易提交。

## 6.4 账户定位冲突处理

1. 同侧仅传 `*_account_no` 或 `*_out_user_id` 时，按既定优先级解析。
2. 同侧同时传两者时：
   - 若最终解析为同一 `account_no`，允许通过（视为一致输入）
   - 若解析结果不一致，返回 `ACCOUNT_RESOLVE_CONFLICT`

## 6.5 交易列表分页

1. 排序键固定：`created_at DESC, txn_no DESC`。
2. 分页方式采用 seek，不使用 offset。
3. `page_token` 编码上一页末条 `(created_at, txn_no)`。
4. 下一页查询条件：`(created_at, txn_no) < (:created_at, :txn_no)`。

---

## 7. 安全设计细节

1. 鉴权头：`X-Merchant-No/X-Timestamp/X-Nonce/X-Signature`
2. 签名串：`METHOD\nPATH\nmerchant_no\ntimestamp\nnonce\nSHA256(body)`
3. 签名算法：`HMAC-SHA256(secret, signing_string)`
4. 防重放：
   - 时间窗 <= 5 分钟
5. 密钥存储：`merchant_api_credential.secret_ciphertext`
   - `key_provider=LOCAL`
   - `kms_key_id=LOCAL_KMS_KEY_V1`
6. 密钥轮转生效：新密钥生效后旧密钥立即失效
7. Webhook 回调签名：V1 复用 API `merchant_secret`

---

## 8. 失败处理与补偿

## 8.1 交易补偿

- 扫描滞留交易（`PROCESSING/PAY_SUCCESS`）
- 根据日志与外部结果推进到可收敛终态

## 8.2 通知补偿

- 扫描 `notify_log.status=FAILED`
- 指数退避重试（如 1m/5m/15m/1h/6h）
- 超限进入 `DEAD`

## 8.3 对账巡检

- 校验账户余额与账户流水一致
- 对 `book_enabled=true` 账户校验：`account.balance == sum(account_book.balance)`

## 8.4 多实例部署约束（分布式运行）

### API 层

1. API 服务可水平扩容，多实例共享 PostgreSQL 与 Redis。
2. 请求幂等仍由数据库唯一键 `UNIQUE (merchant_no, out_trade_no)` 统一约束。
3. 重复请求语义不变：同 key 后续请求统一 HTTP 409，且无副作用。
4. 执行幂等键 `processing_key=txn_no+stage` 在 Redis 全局生效，避免跨实例重复执行阶段逻辑。

### Worker 与定时任务层

1. `robfig/cron` 在多实例下默认会被每个实例触发，必须做“单活调度”。
2. 单活策略可选：
   - 分布式锁（Redis）
   - Leader 选主（任一实例持有执行权）
3. Outbox 拉取建议使用 `FOR UPDATE SKIP LOCKED`，支持多 worker 并行消费且避免抢同一事件。
4. 通知与补偿任务按“至少一次”语义设计，所有处理器必须幂等可重入。

### 运维要求

1. 所有实例必须统一 UTC 与 NTP 时钟同步，避免防重放窗口误判。
2. 必须暴露并监控：
   - cron 锁竞争/持锁时长
   - outbox 堆积量与消费延迟
   - webhook 重复投递率与 DEAD 数量

---

## 9. 实施约束（CR 必卡）

1. 禁止绕过幂等键。
2. 禁止事务外更新余额。
3. 禁止 `book_enabled=false` 账户写入 `account_book` 或 `account_book_change_log`。
4. 禁止明文存储/打印 `merchant_secret`。
5. 禁止新增与 `CODE_RULES.md` 冲突的编码格式。
6. 禁止绕过状态机直接改终态。

---

## 10. 最小测试矩阵（实现验证）

1. 签名/防重放失败路径。
2. 幂等重复与冲突请求。
3. 透支边界（无透支/有限透支/无限透支）。
4. `allow_credit_in/allow_debit_out/allow_transfer` 能力约束。
5. 过期路径 FEFO 与 `expire_at > now_utc` 边界。
6. 并发退款不超退。
7. Outbox 最终投递与通知 DEAD 收敛。
8. 账本汇总与账户余额一致性巡检。
