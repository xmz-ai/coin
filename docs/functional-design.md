# COIN 功能设计（Functional Design）

> 本文定义“系统提供哪些功能、输入输出与行为约束”。
> 与 `requirements-design.md` 对齐，不展开底层实现细节。

---

## 1. 功能架构

系统按业务能力划分为 6 个功能域：

1. **商户与凭证管理**：商户开户、系统账户绑定、密钥轮转。
2. **客户与账户管理**：客户开户、客户查询、账户创建、能力更新、余额查询。
3. **交易处理**：发放/扣减/转账/退款。
4. **幂等与并发控制**：请求幂等、执行幂等、退款 CAS。
5. **事件与通知**：Outbox 事件、Webhook 投递、Webhook 配置管理、重试与死信。
6. **运维治理**：补偿任务、对账巡检、可观测与告警。

---

## 2. 功能清单

## 2.1 商户与凭证管理

### F-01 创建商户
- 输入：商户名称。
- 输出：`merchant_no/merchant_secret(仅一次)/budget_account_no/receivable_account_no`。
- 行为：同事务创建商户、预算账户、收款账户、绑定关系。

### F-02 轮转商户密钥
- 输入：`merchant_no`、轮转原因。
- 输出：新 `merchant_secret`（仅一次）、`secret_version`。
- 行为：新密钥生效后旧密钥立即失效，密钥始终密文存储。

### F-03 查询当前商户配置
- 输入：鉴权上下文（无需 body）。
- 输出：`merchant_no/name/status/budget_account_no/receivable_account_no/secret_version`。
- 约束：仅返回当前鉴权商户信息。

## 2.2 客户与账户管理

### F-04 创建客户
- 输入：`out_user_id`（从鉴权上下文绑定商户）。
- 输出：`customer_no/merchant_no/out_user_id`。
- 约束：`uniq(merchant_no, out_user_id)`。

### F-05 按 out_user_id 查询客户
- 输入：`out_user_id`。
- 输出：`customer_no/merchant_no/out_user_id/status`。
- 约束：仅允许查询当前商户下客户。

### F-06 创建账户
- 输入：`owner_type/(owner_out_user_id 或 owner_customer_no)/account_scene/currency/capability`。
- 输出：`account_no`。
- 约束：owner 合法归属、能力参数合法（透支规则等）。

### F-07 更新账户能力
- 输入：能力字段部分更新。
- 输出：成功响应。
- 约束：审计可追溯。

### F-08 查询账户余额
- 输入：`account_no`。
- 输出：`balance/book_enabled/book_balance_sum`。

## 2.3 交易处理

### F-09 发放（ISSUE）
- 输入：`out_trade_no/amount/expire_in_days(条件必填)` + 出入账账户定位参数。
- 账户定位：借方默认使用商户 `budget_account_no`（可用 `debit_account_no` 显式覆盖）；贷方 `credit_account_no` 或 `user_id` 二选一。
- 输出：`txn_no/status`。
- 约束：借贷能力校验、幂等、状态机推进、流水落库。

### F-10 扣减（CONSUME）
- 输入：`out_trade_no/amount` + 出入账账户定位参数。
- 账户定位：`debit_account_no` 或 `debit_out_user_id` 二选一；`credit_account_no` 或 `credit_out_user_id` 二选一（均未提供时默认转入当前商户 `receivable_account_no`）。
- 输出：`txn_no/status`。
- 约束：透支语义严格执行；账户定位冲突（同侧同时传 `account_no` 与 `out_user_id` 且解析不一致）返回 `ACCOUNT_RESOLVE_CONFLICT`。

### F-11 转账（P2P）
- 输入：`out_trade_no/amount/to_expire_in_days(条件必填)` + 双边账户定位参数。
- 账户定位：`from_account_no` 或 `from_out_user_id` 二选一；`to_account_no` 或 `to_out_user_id` 二选一。
- 输出：`txn_no/status`。
- 约束：转出账户必须 `allow_transfer=true`，借贷同事务；P2P 不走商户默认账户兜底。

### F-12 退款（REFUND）
- 输入：`out_trade_no/refund_of_txn_no/amount`。
- 输出：`txn_no/status`（提交态）。
- 约束：异步阶段校验原单为 `TRANSFER` 且 `RECV_SUCCESS`；并发退款通过 `refundable_amount` CAS 控制不超退。

### F-13 查询交易（单笔）
- 输入：`txn_no` 或 `out_trade_no`。
- 输出：主单状态、明细、错误信息、可退余额。

### F-14 查询交易列表
- 输入：时间范围 + 过滤条件（状态、场景、`out_user_id`）+ 分页参数。
- 输出：交易列表与分页游标。

## 2.4 幂等与并发控制

### F-15 请求幂等
- 键（对外）：`(merchant_no, out_trade_no)`。
- 行为：
  - 同键后续请求统一返回 HTTP 409（`DUPLICATE_OUT_TRADE_NO`）。
  - 不再区分“同键同请求”与“同键异请求”。
  - 重复请求拒绝时不得产生副作用。

### F-16 执行幂等
- 键：`processing_key=txn_no+stage`。
- 策略：全局统一 TTL（配置项，不固化值）。

### F-17 并发控制
- 余额变更：行锁 + 条件更新。
- 退款：`refundable_amount` CAS 递减。

## 2.5 事件与通知

### F-18 Outbox 事件
- 交易终态生成领域事件并与主事务同提交。

### F-19 Webhook 通知
- 事件：`TxnSucceeded/TxnFailed/TxnRefunded`。
- 行为：异步投递，失败指数退避重试，超限进入 DEAD。
- 回调签名：V1 复用 API `merchant_secret`。

### F-20 Webhook 配置管理
- 输入：`url/enabled/retry_policy`。
- 输出：当前商户 Webhook 配置快照。
- 约束：仅允许操作当前商户配置。

## 2.6 运维治理

### F-21 交易补偿
- 扫描滞留状态（如 `INIT/PAY_SUCCESS`）推进到可收敛状态。

### F-22 通知补偿
- 扫描失败通知并重试。

### F-23 对账巡检
- 校验余额与流水一致性。
- `book_enabled=true` 账户校验 account 与 account_book 汇总一致。

---

## 3. 功能规则矩阵（关键）

| 规则 | 功能点 | 约束 |
|---|---|---|
| 幂等唯一 | F-09~F-12 | `uniq(merchant_no, out_trade_no)`（对外语义） |
| 状态机守卫 | F-09~F-12 | 仅允许合法状态跃迁 |
| 账本路径开关 | F-09~F-12 | 仅 `book_enabled=true` 使用 `account_book`（V1 按过期维度） |
| FEFO 扣减 | F-09~F-12 | 过期账户固定先到期先扣 |
| 过期边界 | F-09~F-12 | 仅 `expire_at > now_utc` 可扣减 |
| 安全鉴权 | F-01~F-14 | 签名 + 时间窗校验 |
| 密钥安全 | F-01/F-02 | 密文存储，明文仅一次返回 |

---

## 4. 错误语义（功能层）

核心错误码：
- `INVALID_PARAM`
- `INVALID_SIGNATURE`
- `MERCHANT_NOT_FOUND`
- `MERCHANT_DISABLED`
- `CUSTOMER_NOT_FOUND`
- `CUSTOMER_NOT_BELONG_TO_MERCHANT`
- `ACCOUNT_NOT_FOUND`
- `OUT_USER_ID_NOT_FOUND`
- `ACCOUNT_RESOLVE_CONFLICT`
- `ACCOUNT_DISABLED`
- `ACCOUNT_FORBID_DEBIT`
- `ACCOUNT_FORBID_CREDIT`
- `ACCOUNT_FORBID_TRANSFER`
- `INSUFFICIENT_BALANCE`
- `DUPLICATE_OUT_TRADE_NO`
- `TXN_NOT_FOUND`
- `REFUND_AMOUNT_EXCEEDED`
- `TXN_STATUS_INVALID`
- `INTERNAL_ERROR`

---

## 5. 功能验收用例（最小集合）

1. 鉴权失败：签名错、时间戳过期、鉴权头缺失。
2. 幂等：同 `out_trade_no` 重复请求统一返回 `DUPLICATE_OUT_TRADE_NO`（HTTP 409），且无副作用。
3. 能力约束：禁出账/禁入账/禁转账分别生效。
4. 透支边界：无透支、有限透支、无限透支。
5. 过期路径：
   - `book_enabled=false` 不落 `account_book`。
   - `book_enabled=true` 正确落账本并对齐汇总。
6. 并发退款：总退款额不超过原单可退。
7. 通知重试：失败重试到 SUCCESS 或 DEAD。
