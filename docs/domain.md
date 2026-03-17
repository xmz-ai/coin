# Credits 账务系统领域模型细化（Use Case 驱动）

> 当前已确认原则：
> - 三户一本：客户、账户、商户、账本
> - 商户开户自动绑定预算账户与收款账户
> - 账户支持特性开关（透支/转账/账本模式）
> - 仅 `book_enabled=true` 时启用账本；账户作为父账户，账本作为子账本
> - `book_enabled=false` 时仅账户记账，不落账本
> - V1 的账本用途仅为按过期时间切分；冻结等能力后续通过新增账本类型扩展

---

## 1. 领域边界与上下文

## 1.1 核心上下文（Core Ledger）
负责：
- 账户余额与账本余额管理
- 交易生命周期（建单、执行、状态推进、退款）
- 幂等、审计流水、对账数据

不负责：
- 风控决策引擎（可通过调用外部服务接入）
- 营销规则编排（由上游应用层处理）
- 汇率换算（V1 不支持）

## 1.2 上下文映射
- 上游（调用方）：业务系统（订单、营销、运营）
- 下游（被调用）：Webhook 通知消费者
- 基础设施：PostgreSQL、Redis、Outbox Worker、调度器

## 1.3 商户鉴权上下文
- 每个商户发放一组 API 凭证（`merchant_no` + `merchant_secret`）。
- 所有交易请求在网关/应用层完成签名验签与商户身份确认。
- 交易域内不再引入独立 `app_id`，统一以 `merchant_no` 作为租户与幂等隔离键。
- `merchant_secret` 仅用于鉴权，不落交易主单；服务端在凭证表持久化可解密密文（`secret_ciphertext`），当前采用 `key_provider=LOCAL` + `kms_key_id=LOCAL_KMS_KEY_V1`。

---

## 2. 聚合与实体设计

## 2.1 Merchant 聚合

### 实体：Merchant
**主键/标识**
- `merchant_id`（内部 UUID）
- `merchant_no`（商户编码，对外暴露/关联）

**核心属性**
- `name`
- `status`（ACTIVE/INACTIVE）
- `created_at`
- `updated_at`

**领域规则**
1. 商户创建成功后，必须存在且仅存在一组系统账户绑定（预算 + 收款）。
2. 商户停用后不可发起新交易（可保留查询能力）。

### 实体：MerchantAccountBinding
**标识**
- `merchant_no`（唯一）

**属性**
- `budget_account_no`
- `receivable_account_no`

**不变量**
- `uniq(merchant_no)`
- `budget_account_no != receivable_account_no`
- 两个账号都必须归属该商户（`owner_type=MERCHANT && owner_id=merchant_no`）

> 当前实现中，绑定信息直接内嵌在 `merchant.budget_account_no / merchant.receivable_account_no`，
> 暂未单独拆表 `merchant_account_binding`。

---

## 2.2 Customer 聚合

### 实体：Customer
**标识**
- `customer_id`
- `customer_no`（外部数字编码）

**属性**
- `merchant_no`（归属商户）
- `out_user_id`（商户侧用户ID）
- `status`（ACTIVE/INACTIVE）
- `created_at`
- `updated_at`

**规则**
- Customer 必须归属且仅归属一个 Merchant。
- `out_user_id` 在商户内唯一（`uniq(merchant_no, out_user_id)`）。
- Customer 可拥有多个 Account。
- Customer 停用后禁止新交易，但不影响历史账务查询。

---

## 2.3 Account 聚合（核心）

### 实体：Account
**标识**
- `account_no`

**归属属性**
- `owner_type`（CUSTOMER/MERCHANT）
- `owner_id`
- `account_scene`（BUDGET/RECEIVABLE/CUSTOM）

> 折中方案：`account_scene` 只用于开户模板、运营可读性与报表分组；
> 不参与交易可否执行的判定。交易能力统一由 `AccountCapability` 决定。

**金额属性**
- `balance`（账户汇总余额，BIGINT）

**能力属性（Capability）**
- `allow_overdraft`（bool）
- `max_overdraft_limit`（BIGINT）
- `allow_transfer`（bool）
- `book_enabled`（bool）

**状态属性**
- `status`（ACTIVE/INACTIVE/CLOSED）
- `created_at`
- `updated_at`

### 值对象：AccountCapability
- `allow_overdraft`
- `max_overdraft_limit`
- `allow_transfer`
- `allow_credit_in`（是否支持入账）
- `allow_debit_out`（是否支持出账）
- `book_enabled`

说明：
- `book_enabled` 表示“是否启用父账户+子账本模式”，不是“是否已经存在 book 数据”。
- `book_enabled=true` 时仍允许懒创建账本（首次入账再建 book）。

### Account 关键不变量
1. `allow_overdraft=false` 时，不允许透支（扣减必须满足 `balance >= amount`）。
2. `allow_overdraft=true` 时：
   - `max_overdraft_limit > 0`：表示有限透支上限；
   - `max_overdraft_limit = 0`：表示无限透支。
3. `max_overdraft_limit` 不允许小于 0。
4. `allow_credit_in=false` 的账户禁止作为入账方。
5. `allow_debit_out=false` 的账户禁止作为出账方。
6. `status != ACTIVE` 的账户禁止参与借贷方向记账。
7. 若 `book_enabled=false`，该账户不允许出现任何 `account_book` 记录。
8. 若 `book_enabled=true`，账户可关联多个账本，且账本只允许一级。

---

## 2.4 AccountBook 聚合（账本模式账户，V1=过期维度）

### 实体：AccountBook
**标识**
- `book_no`

**归属属性**
- `account_no`
- `expire_at`（到期时间，V1 非空）

**金额属性**
- `balance`（BIGINT）

**状态属性（可选）**
- `status`（ACTIVE/EXPIRED）
- `created_at`
- `updated_at`

### AccountBook 不变量
1. `uniq(account_no, expire_at)`：每个过期时间一个账本。
2. 账本不能再挂子账本（模型上无 parent_book_no）。
3. 账本余额汇总应等于该账户所有账本余额总和：
   - `account.balance = Σ account_book.balance(account_no)`（仅对 `book_enabled=true` 账户成立）。
4. V1 不支持冻结状态字段；后续冻结能力通过新增账本类型与账本间转移扩展。

---

## 2.5 Transaction 聚合

### 实体：Txn（交易主单）
**标识**
- `txn_no`

**幂等属性**
- `merchant_no`
- `out_trade_no`（`uniq(merchant_no, out_trade_no)`）

**业务属性**
- `biz_type`（TRANSFER/REFUND）
- `transfer_scene`（ISSUE/CONSUME/P2P/ADJUST；仅 `biz_type=TRANSFER` 时有值）
- `debit_account_no`（交易借方账户）
- `credit_account_no`（交易贷方账户）
- `amount`
- `status`（INIT/PROCESSING/PAY_SUCCESS/RECV_SUCCESS/FAILED）
- `refund_of_txn_no`（退款单关联）
- `refundable_amount`
- `error_code`
- `error_msg`
- `created_at`
- `updated_at`

**边界说明**
- `txn` 的唯一职责是记录“商户侧发起的交易语义”：
  - 交易双方、金额、状态、可退金额、幂等关系。
- `txn` 不承载实际资金路由细节（如 book 级拆分）；资金流向由流水表表达：
  - `account_change_log`
  - `account_book_change_log`（仅过期账户）

### 实体：TxnDetail（暂不启用）
- 本期不支持聚合支付/多账户拆分交易，故不建 `txn_detail`。
- 后续仅在“单笔交易需要多条账户明细”时再引入 `txn_detail`（一单多明细）。

### Txn 不变量
1. `amount > 0`
2. 退款总额不超过原单可退金额：`sum(refunds) <= original.refundable_amount_initial`
3. `biz_type=TRANSFER` 时 `transfer_scene` 必须有值；`biz_type=REFUND` 时 `transfer_scene` 必须为空
4. `biz_type=REFUND` 时 `refund_of_txn_no` 必填
5. `refundable_amount >= 0`

---

## 2.6 审计与集成模型

### 实体：AccountChangeLog
- 记录账户级变更前后余额
- 所有账户都必须落该流水

### 实体：AccountBookChangeLog
- 仅 `book_enabled=true` 账户落账本级流水
- V1 用于恢复“按过期时间”余额轨迹

### 实体：OutboxEvent
- 交易域事件（如 `TxnSucceeded`、`TxnFailed`）
- 与业务事务同库同事务提交

### 实体：NotifyLog
- Webhook 通知结果
- 重试次数、下次重试时间、最终状态

---

## 3. 领域服务（Domain Service）

## 3.1 MerchantOnboardingService
职责：
- 商户开户事务编排（商户 + 预算账户 + 收款账户 + 绑定）

关键规则：
- 原子性提交；任一步失败整体回滚。

## 3.2 AccountingService
职责：
- 扣减、发放、转账、退款核心规则校验与余额变更

关键规则：
- 先校验能力（透支/转账/状态）
- 再执行余额变化（账户路径 or 账本路径）
- 最后写流水与推进状态

## 3.3 BookRoutingService（V1: Expiry）
职责：
- `book_enabled=true` 账户下选择参与记账的账本集合

策略（已拍板）：
- FEFO（先到期先扣）
- 入账写入指定 `expire_at` 对应账本（`expire_at` 必填）

## 3.4 RefundService
职责：
- 原单可退金额校验、退款账户集合校验、余额回补

关键规则：
- `refundable_amount` CAS 递减控制并发退款

---

## 4. Use Case 推演（从场景到数据流转）

## UC-01 商户开户（自动绑定预算/收款）

### 触发
运营/系统创建新商户。

### 前置
- 商户编码未被占用。

### 流程
1. 创建 `merchant`
2. 创建预算账户（`account_scene=BUDGET`）
3. 创建收款账户（`account_scene=RECEIVABLE`）
4. 创建 `merchant_account_binding`
5. 提交事务

### 数据流
- 写入：`merchant`、`account`(2条)、`merchant_account_binding`
- 输出：`merchant_no` + 两个系统账号

### 后置不变量
- `merchant_no` 存在唯一绑定关系
- 两个账户均归属于该商户

---

## UC-02 客户开户与多账户创建

### 触发
业务为客户创建主账户或场景账户。

### 前置
- customer 状态 ACTIVE

### 流程
1. 创建/校验 `customer`
2. 创建 `account`（可指定 capability）
3. 若 `book_enabled=true`，不强制预建账本（可按入账懒创建）

### 数据流
- 写入：`customer`、`account`

### 后置
- customer 可绑定多个 account

---

## UC-03 发放（Credit）

### 触发
商户向客户发放 credits。

### 前置
- `uniq(merchant_no, out_trade_no)` 不冲突
- 借方账户（通常商户预算账户）满足扣减条件
- 贷方账户状态 ACTIVE

### 流程
1. 创建 `txn(status=INIT)`（主单记录双方账户、金额、幂等键）
2. 状态 `INIT -> PROCESSING`
3. 扣借方：
   - 非过期账户：直接减 `account.balance`
   - 过期账户：按路由规则减 `account_book.balance`，同步更新 `account.balance`
4. 入贷方：
   - 非过期账户：加 `account.balance`
   - 过期账户：按 `expire_at` 写入/更新 `account_book`，同步更新 `account.balance`
5. 写 `account_change_log`；必要时写 `account_book_change_log`
6. 状态 `PROCESSING -> PAY_SUCCESS -> RECV_SUCCESS`
7. 写 `outbox_event`（成功事件）

### 数据流
- 写：`txn`、`account`/`account_book`、`account_change_log`、`account_book_change_log`(可选)、`outbox_event`
- 异步：Worker 读取 outbox，投递 Webhook，写 `notify_log`

---

## UC-04 扣减（Debit）

### 触发
从客户账户扣减 credits（消费/核销）。

### 前置
- 幂等通过
- 账户能力允许扣减（透支规则）

### 流程
与 UC-03 类似，交易主单仍记录借贷双方账户（可能是显式传入或默认系统账户）。

### 关键规则
- 出账能力：`allow_debit_out=true`
- `allow_overdraft=false`：`balance >= amount`
- `allow_overdraft=true && max_overdraft_limit > 0`：`balance + max_overdraft_limit >= amount`
- `allow_overdraft=true && max_overdraft_limit = 0`：视为无限透支，不做上限拦截（仍保留风控/审计）。

---

## UC-05 转账（Transfer）

### 触发
客户 A 账户转入客户 B 账户。

### 前置
- 转出账户 `allow_transfer=true`
- 两个账户币种/账务类型兼容（V1 假设同币种）

### 流程
1. 建单 + 幂等
2. 转出侧扣减（账户/账本路径）
3. 转入侧入账（账户/账本路径）
4. 双边流水 + 状态推进 + outbox

### 一致性要求
- 转出与转入必须在同一 DB 事务内完成。

---

## UC-06 退款（Refund）

### 触发
对原交易进行部分/全额退款。

### 前置
- 原单存在且终态可退
- 原单可退余额足够

### 流程
1. 创建退款 `txn`（关联 `refund_of_txn_no`）
2. CAS 扣减原单 `refundable_amount`
3. 按原路径反向记账（账户/账本）
4. 写流水并推进退款交易状态
5. outbox + Webhook 通知

### 关键约束
- 并发退款下，CAS 失败必须快速返回并重试/失败。

---

## UC-07 账本模式记账（book_enabled=true，V1=过期）

### 触发
对 `book_enabled=true` 账户发起扣减/入账。

### 扣减路径
1. 查询账户下有效账本（`expire_at > now_utc`）
2. 按 FEFO 排序扣减
3. 多账本拆分生成多条 `account_book_change_log`
4. 账户汇总余额同步减少

### 入账路径
1. 根据入账请求 `expire_at` 定位账本，不存在则创建
2. 更新该账本余额
3. 同步更新账户汇总余额

### 约束
- 任何时候禁止二级账本结构。

---

## UC-08 非账本模式记账（book_enabled=false）

### 触发
对普通账户发起扣减/入账。

### 路径
- 仅更新 `account.balance`
- 仅写 `account_change_log`
- 交易主单不记录 `book_no`，book 级路由信息仅落账本流水

---

## UC-09 通知重试（Webhook）

### 触发
outbox 事件投递失败或超时。

### 流程
1. 记录 `notify_log` 失败
2. 计算 `next_retry_at`（指数退避）
3. 调度器捞取到期失败记录再投递
4. 达到最大重试后标记 DEAD

---

## 5. 关键数据流与状态流总览

## 5.1 命令主线
`API Request -> Idempotency Check -> Txn Init -> Command Execute -> Balance Mutation -> Logs -> Status Final -> Outbox -> Webhook`

## 5.2 状态流
- `INIT -> PROCESSING -> PAY_SUCCESS -> RECV_SUCCESS`
- 任一阶段异常：`-> FAILED`

## 5.3 记账路径分流
- `book_enabled=false`：Account Path
- `book_enabled=true`：AccountBook Path + Account Summary Sync（V1 按过期维度）

---

## 6. 领域事件建议

- `MerchantOpened`
- `MerchantSystemAccountsBound`
- `AccountCreated`
- `TxnCreated`
- `TxnProcessingStarted`
- `TxnPaid`
- `TxnReceived`
- `TxnFailed`
- `TxnRefunded`
- `NotifySucceeded`
- `NotifyFailed`

每个事件最少携带：
- `event_id`
- `event_type`
- `occurred_at`
- `txn_no`（有交易时）
- `merchant_no`
- `out_trade_no`
- `operator/system_source`

---

## 7. 一致性与并发控制落点

1. 余额更新：`SELECT ... FOR UPDATE` + 条件更新
2. 幂等：
   - 业务幂等：`uniq(merchant_no, out_trade_no)`
   - 执行幂等：`processing_key`（Redis）
3. 退款并发：`refundable_amount` CAS
4. 外部副作用：Outbox 与主交易同事务

---

## 8. 已拍板实现策略（影响代码实现）

1. 幂等执行键（`processing_key`）TTL：全局统一 TTL（不按 `biz_type` 分配）。
2. 透支默认策略：默认关闭；但商户初始预算账户例外，默认 `allow_overdraft=true && max_overdraft_limit=0`（无限透支）。
3. `book_enabled` 仅表示是否启用子账本模式，不表示是否已有 book 数据。
4. V1 账本扣减策略：固定 FEFO（按 `expire_at`）。
5. V1 账本入账策略：必须显式传 `expire_at`（不做默认 TTL 推导）。

---

## 9. 建议的代码映射（Go）

- `domain/merchant.go`
  - `type Merchant`
  - `type MerchantAccountBinding`
- `domain/customer.go`
  - `type Customer`
- `domain/account.go`
  - `type Account`
  - `type AccountCapability`
  - `func (a *Account) CanDebit(amount int64) error`
  - `func (a *Account) CanTransfer() error`
- `domain/account_book.go`
  - `type AccountBook`
  - `type BookSelector interface { SelectForDebit(...) }`
- `domain/transaction.go`
  - `type Txn`
  - `func (t *Txn) Transit(to TxnStatus) error`
- `domain/state_machine.go`
  - 状态迁移矩阵与校验器

---

## 10. 验证清单（建模完成后必须通过）

1. `book_enabled=false` 账户交易不会产生任何账本数据。
2. `book_enabled=true` 账户的账本余额汇总恒等于账户余额。
3. 商户开户后一定存在预算+收款绑定，且不可重复绑定。
4. 转账受 `allow_transfer` 严格约束。
5. 透支规则与 `max_overdraft_limit` 一致。
6. 退款并发下不会超过原单可退额度。
7. 任意失败场景均可通过日志追溯到 `txn_no/out_trade_no/account_no`。

---

## 11. Legacy 对照（coin-core -> 新领域模型）

> 目的：把当前 `coin-core` 的成熟经验显式映射到新模型，避免重构时丢失关键规则。

### 11.1 账户类型映射建议

`coin-core` 当前账户大类（`AccountClassEnum`）包括：
- `POOL`、`BUDGET`、`PUBLIC`、`PERSONAL`、`COLLECTION`、`RECYCLE`

新模型建议：
- 账户归属继续使用 `owner_type + owner_id`（CUSTOMER/MERCHANT）
- 账户场景使用 `account_scene` 吸收 legacy 语义：
  - `BUDGET`（预算）
  - `RECEIVABLE`（收款）
  - `CUSTOM`（普通业务账户，可覆盖个人/公共场景）
  - 可选预留：`POOL`、`RECYCLE`

迁移原则：
1. `BUDGET/COLLECTION` 必须可映射到商户系统账户（开户自动创建）。
2. legacy 的 `PERSONAL/PUBLIC` 差异，不通过“是否有账本”硬编码区分，而通过 `book_enabled` 显式控制。
3. 禁付/可转账等能力，不由账户类型推断，全部落到 capability 字段。

### 11.2 子账户类型与币种借鉴

`coin-core` 有 `SubAcctType` 与 `Currency` 体系（普通币、珍珠现金/奖励等）。

新模型建议：
- V1 最小闭环字段：
  - `currency`
  - `account_scene`
- V1.1 可扩展：
  - `sub_account_type`（用于产品线/资产桶隔离）

约束建议：
- 同一交易中借贷双方必须币种一致（V1）。
- `sub_account_type` 若启用，需有“兼容矩阵”校验。

### 11.3 账务类型映射建议

`coin-core/BizTypeEnum` 的可借鉴点：
- 每个 biz type 附带元信息：
  - `is_revoke`
  - `original_biz_type`
  - `need_assign_revoke_amount`
  - `support_partial_refund`

新模型建议引入：
- `TxnBizType`（资金动作）
  - `TRANSFER`、`REFUND`
- `TransferScene`（TRANSFER 的业务场景）
  - `ISSUE`（发放）
  - `CONSUME`（扣减）
  - `P2P`（账户间转账）
  - `ADJUST`（运营调账）
- `AccountOpType`（账户管理动作）
  - 如账户启停、能力变更等

并保留 metadata 机制：
- 退款/回滚类型是否支持部分退款
- 是否需要指定退款明细（按账户拆分）

### 11.4 路径分流借鉴

`coin-core` 实际做了四类路径分流（acct->acct, acct->book, book->book, book->acct）。

新模型落地：
- 仍保留“账户路径/账本路径”分流能力
- 但分流条件统一为 `book_enabled`，不再按账户大类硬编码
- 对于混合路径（`book_enabled=true/false` 账户互转）明确支持；book 级拆分只体现在流水，不写入交易主单

---

## 12. Use Case 输入/输出契约（Service 层接口草案）

> 本节用于把领域规则下沉到接口契约，便于后续 API/Service 实现。

### 12.1 UC-01 商户开户

**Input**
- `name`
- `operator`

**Output**
- `merchant_no`
- `merchant_secret`（仅返回一次）
- `budget_account_no`
- `receivable_account_no`

**校验**
- `merchant_no` 唯一
- 绑定关系唯一且预算/收款账号不同

### 12.2 UC-03 发放（Credit）

**Input**
- `merchant_no`
- `out_trade_no`
- `debit_account_no`（通常预算账户）
- `credit_account_no`
- `amount`
- `biz_type=TRANSFER`
- `transfer_scene=ISSUE`
- `expire_at`（当入账方 `book_enabled=true` 时必填）

**Output**
- `txn_no`
- `status`
- `debit_after_balance`
- `credit_after_balance`

**校验**
- 幂等唯一键不冲突
- `amount > 0`
- 借方 `allow_debit_out=true` 且余额/透支能力满足
- 贷方 `allow_credit_in=true`
- 过期账户入账需满足账本规则

### 12.3 UC-04 扣减（Debit）

**Input**
- `merchant_no`
- `out_trade_no`
- `debit_account_no`
- `amount`
- `biz_type=TRANSFER`
- `transfer_scene=CONSUME`

**Output**
- `txn_no`
- `status`
- `debit_after_balance`

**校验**
- 出账能力：`allow_debit_out=true`
- 透支能力规则（`allow_overdraft/max_overdraft_limit`）
- 非过期账户不得产生 `account_book_change_log`

### 12.4 UC-05 转账（Transfer）

**Input**
- `merchant_no`
- `out_trade_no`
- `from_account_no`
- `to_account_no`
- `amount`
- `biz_type=TRANSFER`
- `transfer_scene=P2P`
- `to_expire_at`（当目标账户 `book_enabled=true` 时必填）

**Output**
- `txn_no`
- `status`
- `from_after_balance`
- `to_after_balance`

**校验**
- `from.allow_transfer=true`
- `from.allow_debit_out=true`
- `to.allow_credit_in=true`
- 币种一致
- 转出转入同事务

### 12.5 UC-06 退款（Refund）

**Input**
- `merchant_no`
- `out_trade_no`
- `refund_of_txn_no`
- `amount`
- `biz_type=REFUND`

**Output**
- `txn_no`
- `status`
- `refunded_amount`
- `origin_refundable_left`

**校验**
- 原单可退
- `amount <= origin.refundable_amount`
- 并发下 CAS 保证不超退

---

## 13. 迁移时必须保留的规则清单（来自 coin-core）

1. **双层幂等**：请求幂等 + 执行幂等必须同时存在。
2. **退款并发控制**：原单 `txn.refundable_amount` 必须 CAS 递减，保证并发不超退。
3. **账户/账本双流水**：过期账户需同时有账户汇总流水与账本流水。
4. **状态机守卫**：禁止绕过状态机直接改终态。
5. **补偿闭环**：交易补偿与通知重试都要保留。
6. **可观测字段统一**：日志/事件至少包含 `txn_no/merchant_no/out_trade_no/account_no`。
