# COIN 需求设计（Requirements Design）

> 本文定义“做什么、为什么做、做到什么程度”。
> 方案边界与术语与 `docs/handoff.md` 保持一致。

---

## 1. 背景与目标

当前需要建设 Go 版 COIN 账务系统，满足以下核心目标：

1. **高一致性**：余额、流水、交易状态一致且可审计。
2. **高可用**：故障可补偿、通知可重试、系统可收敛。
3. **可扩展**：支持多账户能力开关与交易场景扩展。
4. **可观测**：关键链路具备日志、指标、追踪与告警。

---

## 2. 业务范围（V1）

### 2.1 In Scope

1. 三户一本模型：`Merchant / Customer / Account / AccountBook`。
2. 商户开户原子化：自动创建并绑定预算账户与收款账户。
3. 账户能力开关：
   - `allow_overdraft`
   - `max_overdraft_limit`
   - `allow_transfer`
   - `allow_credit_in`
   - `allow_debit_out`
   - `book_enabled`
4. 交易能力：`TRANSFER`（`ISSUE/CONSUME/P2P/ADJUST`）与 `REFUND`。
5. 双层幂等：
   - 请求幂等（对外）：`(merchant_no, out_trade_no)`
   - 执行幂等：`processing_key`（全局统一 TTL，配置化，不固定值）
6. Outbox + Webhook 通知 + 重试补偿。
7. 核心审计流水与对账能力。
8. 接入方自助查询与配置：商户基础配置查询、按 `out_user_id` 客户查询、交易列表查询、Webhook 配置管理。

### 2.2 Out of Scope（V1 不做）

1. 冻结/解冻/capture。
2. 跨币种汇兑。
3. 跨地域强一致分布式事务。
4. 全量自动历史迁移。

---

## 3. 核心角色与场景

### 3.1 角色

1. **商户系统**：发起开户、交易、退款、查询。
2. **账务系统**：执行记账、状态推进、事件投递。
3. **Webhook 消费方**：接收交易结果回调。
4. **运维/风控/运营**：巡检、补偿、排障、审计。

### 3.2 核心业务场景

1. 商户开户（自动绑定预算/收款账户）
2. 客户开户与多账户创建
3. 发放（Issue）
4. 扣减（Consume）
5. 转账（P2P）
6. 退款（Refund）
7. 过期账户记账（`book_enabled=true`，FEFO）
8. 非过期账户记账（`book_enabled=false`）

---

## 4. 关键业务规则

1. **账本启用条件**：仅 `book_enabled=true` 的账户可使用 `account_book`。
   - `book_enabled` 是模式开关（父账户+子账本），不是“是否已有账本数据”。
2. **账本层级**：仅一级账本，不允许二级账本。
3. **过期扣减规则**：固定 FEFO，且仅 `expire_at > now_utc` 账本可扣减。
4. **过期账户入账规则**：`expire_at` 必填。
   - V1 暂不支持冻结/解冻，后续可通过新增账本类型扩展。
5. **透支规则**：
   - 默认 `allow_overdraft=false`
   - `allow_overdraft=true && max_overdraft_limit>0` 为有限透支
   - `allow_overdraft=true && max_overdraft_limit=0` 为无限透支
6. **交易幂等语义**：同 `(merchant_no, out_trade_no)` 的后续重复请求一律返回 HTTP 409（不支持重复下单），且不得产生副作用。
7. **发放（ISSUE）默认出账语义**：未显式提供借方账户时，默认使用当前商户预算账户（`budget_account_no`）。
8. **扣减（CONSUME）流向语义**：未显式提供贷方账户时，默认转入当前商户收款账户（`receivable_account_no`）。
9. **交易账户定位方式**：在现有交易接口上扩展 `out_user_id` 账户定位能力，不新增平行交易路由；`account_no` 与 `out_user_id` 可二选一（`out_user_id` 仅用于定位商户下客户账户）。
10. **退款上限**：并发场景下总退款额不得超过原单可退金额（CAS 保证）。
11. **敏感信息安全**：`merchant_secret` 不可明文落库，不可明文日志。
12. **Webhook 签名密钥**：V1 复用 API `merchant_secret`，不单独引入 webhook 独立密钥。
13. **密钥轮转生效规则**：新密钥生效后旧密钥立即失效。

---

## 5. 非功能需求

### 5.1 一致性

1. 余额更新必须在数据库事务内。
2. 禁止在事务外更新账户余额。
3. 状态迁移必须经过状态机守卫。
4. `book_enabled=true` 账户需满足：`account.balance = Σ account_book.balance`（巡检对账保障）。

### 5.2 可用性与恢复

1. Outbox 保证外部副作用最终可达。
2. 交易补偿与通知补偿可推进卡单到终态/死信终态。

### 5.3 安全

1. 鉴权：`merchant_no + merchant_secret` HMAC 签名。
2. 防重放：`timestamp` 窗口校验（<= 5 分钟），`nonce` 仅参与签名。
3. 敏感字段脱敏日志。

### 5.4 可观测

1. 日志最小字段：`txn_no/merchant_no/out_trade_no/account_no/request_id`。
2. 指标：成功率、失败率、状态滞留、重试次数、锁等待。
3. 告警：交易滞留、Outbox 堆积、通知死信、对账不平。

---

## 6. 对外契约要求

1. API 前缀：`/api/v1`
2. Header：`X-Merchant-No/X-Timestamp/X-Nonce/X-Signature`
3. 响应统一结构：`code/message/request_id/data`
4. 金额统一最小货币单位 int64（BIGINT）

---

## 7. 验收标准（Go/No-Go）

1. **功能闭环**：开户、交易、退款、查询主路径可用。
2. **幂等正确**：重复请求结果一致，冲突语义正确。
3. **一致性正确**：并发下余额与流水一致、退款不超退。
4. **过期逻辑正确**：FEFO 与过期边界行为正确。
5. **安全达标**：签名、防重放、密钥密文存储达标。
6. **稳定性达标**：Outbox/通知重试/补偿链路可收敛。

---

## 8. 术语与权威来源

实现与评审采用分层口径：

1. 业务口径层：`docs/requirements-design.md`、`docs/functional-design.md`、`docs/detailed-design.md`
2. 实现口径层：`docs/DDL.md`、`docs/API.md`、`docs/CODE_RULES.md`
3. 交接摘要：`docs/handoff.md`（用于执行与评审对齐，不单独覆盖上述分层规则）

冲突处理：
1. 同层冲突先走 `[Spec Change]` 修文档，再改实现。
2. 跨层冲突先修业务口径层，再同步实现口径层。
