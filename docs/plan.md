# Go 版通用账务系统（Credits）实施方案

## 1. 目标与范围

### 1.1 目标
基于当前项目 `coin-core` 的成熟能力，设计并落地一个 Go 版通用账务系统，优先服务 credits 管理场景，满足：
- 高一致性（余额正确、可追溯）
- 高可用（失败可补偿、可重试）
- 可扩展（多业务类型、多账户模型）
- 可观测（链路、状态、重试可监控）

### 1.2 范围（V1）
- 三户一本模型：客户、账户、商户、账本
- 商户开户自动创建并绑定两个系统账户：预算账户、收款账户
- 客户多账户模型（账户特性可配置）
- 交易能力：扣减、发放、转账、退款
- 请求幂等与执行幂等
- 交易流水与通知机制
- 调度补偿机制（交易补偿 + 通知补偿）

### 1.3 暂不支持（V1）
- 冻结、解冻、冻结扣款（capture）
- 复杂跨币种汇兑
- 多租户跨地域强一致事务
- 全量历史账目迁移自动化（仅预留迁移接口）

---

## 2. 现有系统可复用结论（来自当前项目分析）

### 2.1 应保留的核心设计
1. **命令化执行模型（Request + Command）**
   - API/MQ 入站先建单，再提交命令执行。
   - 优点：天然支持异步化、重试、解耦。

2. **双层幂等机制**
   - 业务幂等：`merchant_id + out_trade_no`
   - 执行幂等：`processing_key`（Redis/DB）

3. **并发一致性底座**
   - 行锁（`SELECT ... FOR UPDATE`）
   - 条件更新/CAS（如 `balance >= amount`、`refundable_amount` 递减）

4. **补偿体系**
   - 交易调度补偿（推进卡单状态）
   - 通知重试补偿（失败退避重试）

5. **审计与追溯模型**
   - 主单、明细、账户流水、账本流水、通知日志完整留痕。

### 2.2 需优化的部分
1. 状态机分散在多服务，建议集中管理。
2. 账户模型需要显式支持“账户特性”配置。
3. 幂等 key TTL 固定，建议按业务类型配置化。
4. 线程隔离与路由策略可观测性不足。
5. 通知与业务事务耦合点可进一步通过 Outbox 规范化。

---

## 3. Go 版目标架构

### 3.1 分层
- `api`：HTTP 入站、参数校验、鉴权
- `application`：交易编排（建单、幂等、提交流程）
- `domain`：客户、商户、账户、账本、交易、状态机
- `infrastructure`：MySQL、Redis、Outbox、调度器
- `worker`：命令执行器、通知执行器、补偿任务

### 3.2 核心流程（统一）
1. 接收请求并做业务幂等检查
2. 创建交易主单 + 明细（同事务）
3. 提交命令（可立即执行或异步队列）
4. 命令执行：锁账户/账本、更新余额、写流水、推进状态
5. 触发通知（Outbox -> Webhook）
6. 调度器补偿卡住的交易与通知

### 3.3 一致性原则
- 所有余额变更必须在 DB 事务中完成。
- 余额更新必须有前置条件（CAS）。
- 主单状态推进必须校验合法状态跃迁。
- 任何外部副作用（通知、事件）必须经 Outbox 保证最终投递。

---

## 4. 领域模型（按你的设计原则）

### 4.1 三户一本模型
1. **客户（Customer）**
   - 业务资金拥有方，可绑定多个账户。

2. **账户（Account）**
   - 余额载体，归属客户或商户。
   - 每个账户具备独立特性配置（见 4.3）。

3. **商户（Merchant）**
   - 业务结算主体。
   - 商户开户成功后，系统自动创建并绑定：
     - 预算账户（Budget Account）
     - 收款账户（Receivable Account）

4. **账本（Ledger Book）**
   - 账本本质上是“账户按过期时间切分后的子账户”。
   - 仅当账户开启 `support_expiry=true` 时，该账户下才会有多个账本（每个过期时间一个账本）。
   - 当 `support_expiry=false` 时，不创建账本，直接在账户维度记账与记流水。
   - 账本只有一级，不允许“账本下再分子账本”。

### 4.2 自动开户规则（商户）
- 创建商户时，在同一事务内：
  1) 创建商户主记录
  2) 创建预算账户
  3) 创建收款账户
  4) 建立商户与两个账户的绑定关系
- 失败则整体回滚，保证商户与系统账户绑定原子性。

### 4.3 账户特性模型（Account Capability）
每个账户至少支持以下开关：
- `allow_overdraft`：是否支持透支
- `allow_transfer`：是否允许转账
- `allow_credit_in`：是否支持入账
- `allow_debit_out`：是否支持出账
- `support_expiry`：是否支持过期（为 true 时才启用子账本）

可扩展字段（预留）：
- `max_overdraft_limit`（>0 表示有限透支上限；=0 且 allow_overdraft=true 表示无限透支）
- `currency`

补充约定：
- `account_scene` 保留，但仅用于开户模板、运营可读性与报表分组。
- 交易可否执行由 capability 决定，不由 `account_scene` 决定。

---

## 5. 数据模型设计（V1）

### 5.1 核心表
1. `merchant`
   - `merchant_id`（唯一）
   - `name`
   - `status`

2. `customer`
   - `customer_id`（唯一）
   - `merchant_id`（归属商户）
   - `status`

3. `account`
   - `account_no`（唯一）
   - `owner_type`（CUSTOMER/MERCHANT）
   - `owner_id`
   - `account_scene`（BUDGET/RECEIVABLE/CUSTOM）
   - `balance`
   - `allow_overdraft`
   - `allow_transfer`
   - `support_expiry`
   - `max_overdraft_limit`
   - `status`

4. `merchant_account_binding`
   - `merchant_id`
   - `budget_account_no`
   - `receivable_account_no`
   - 唯一约束：`uniq(merchant_id)`

5. `merchant_api_credential`
   - `merchant_id`（唯一）
   - `secret_hash`
   - `secret_version`
   - `status`
   - `created_at/updated_at`

   说明：`merchant_secret` 明文仅在开户时返回一次，库内只保存 hash。

6. `ledger_book`（仅 `support_expiry=true` 账户使用）
   - `book_no`（唯一）
   - `account_no`
   - `expire_at`（非空；每个过期时间对应一个账本）
   - `balance`
   - 唯一约束：`uniq(account_no, expire_at)`

6. `txn`
   - `txn_no`（系统交易号，唯一）
   - `merchant_id`
   - `out_trade_no`（与 merchant_id 组合唯一）
   - `biz_type`
   - `amount`
   - `status`
   - `refund_of_txn_no`（退款关联）
   - `refundable_amount`
   - `error_code/error_msg`
   - `created_at/updated_at`

7. `txn_detail`
   - `txn_no`
   - `debit_account_no`
   - `credit_account_no`
   - `debit_book_no`（可空，仅过期账户扣减时有值）
   - `credit_book_no`（可空，仅过期账户入账时有值）
   - `amount`
   - `status`
   - `refundable_amount`

8. `account_change_log`
   - 账户级流水（变更前后余额、操作类型、关联 txn）
   - 所有账户都记录（含支持过期与不支持过期）

9. `book_change_log`（仅 `support_expiry=true` 账户使用）
   - 账本级流水（按 `expire_at` 子账户记录变更轨迹）

10. `outbox_event`
    - 领域事件/通知事件持久化，供异步投递

11. `notify_log`
    - 回调通知历史、重试次数、下次重试时间

### 5.2 索引与约束
- `uniq(merchant_id, out_trade_no)`：业务幂等兜底
- `uniq(txn_no)`：交易唯一
- `idx(status, updated_at)`：补偿扫描
- 退款关联索引：`idx(refund_of_txn_no)`
- `idx(owner_type, owner_id)`：账户归属查询
- `uniq(account_no, expire_at)`：同一账户下“每个过期时间一个账本”
- 约束：`support_expiry=false` 账户禁止写入 `ledger_book` / `book_change_log`
- 约束：账本仅一级，不允许账本再关联子账本
- 所有金额字段使用 `BIGINT`（最小货币单位），禁止浮点

---

## 6. 状态机设计

### 6.1 交易状态
- `INIT`：建单成功，未执行
- `PROCESSING`：命令处理中
- `PAY_SUCCESS`：扣减成功
- `RECV_SUCCESS`：入账成功（终态）
- `FAILED`：失败（终态）

### 6.2 状态迁移规则（示例）
- `INIT -> PROCESSING`
- `PROCESSING -> PAY_SUCCESS|FAILED`
- `PAY_SUCCESS -> RECV_SUCCESS|FAILED`

> 规则由统一组件 `StateMachine` 校验，禁止绕过直接更新状态。

---

## 7. 关键能力设计

### 7.1 幂等
- 入站幂等：`merchant_id + out_trade_no`
- 执行幂等：`processing_key=txn_no+stage`
- 幂等 TTL 配置化（按 biz_type）

### 7.2 账户特性约束
- 入/出账校验：
  - `allow_debit_out=false` 的账户禁止作为出账方
  - `allow_credit_in=false` 的账户禁止作为入账方
- 透支校验：
  - `allow_overdraft=false` 时，要求 `balance >= amount`
  - `allow_overdraft=true && max_overdraft_limit > 0` 时，要求 `balance + max_overdraft_limit >= amount`
  - `allow_overdraft=true && max_overdraft_limit = 0` 时，表示无限透支（仍需风控与审计）
- 转账校验：
  - `allow_transfer=false` 的账户禁止作为转出账户
- 过期账本校验：
  - `support_expiry=true`：账户下启用多个账本（按 `expire_at` 切分），交易在账本维度记账并同步到账户汇总余额
  - `support_expiry=false`：不使用账本，交易直接在账户维度记账与记流水
  - 账本仅一级，不允许二级账本
- 场景约束：
  - `account_scene` 不参与交易能力判定，仅用于模板与运营视图

### 7.3 退款
- 原单 `refundable_amount` CAS 递减
- 支持部分退款与多次退款
- 严格校验退款总额 <= 原单可退金额

### 7.4 补偿
- 交易补偿：定时扫描 `PROCESSING/PAY_SUCCESS`
- 通知补偿：指数退避 + 最大重试次数 + 死信处理

### 7.5 可观测性
- 指标：QPS、失败率、状态滞留、重试次数、锁等待时间
- 日志：必须携带 `txn_no/out_trade_no/account_no`
- Trace：全链路 trace_id 透传

---

## 8. 模块与目录建议（Go）

```text
/credits-ledger
  /cmd
    /api-server
    /worker
  /internal
    /api
    /application
      transaction_service.go
      merchant_onboarding_service.go
    /domain
      customer.go
      merchant.go
      account.go
      ledger_book.go
      transaction.go
      state_machine.go
    /infrastructure
      /db
      /redis
      /outbox
    /worker
      command_executor.go
      notify_executor.go
      scheduler.go
  /migrations
  /configs
  /test
```

---

## 9. 分阶段实施计划

### Phase 0：基线与建模（先行）
- 明确 credits 业务清单与交易类型字典
- 三户一本领域模型评审与拍板
- 商户开户自动绑定账户流程评审
- 账户特性规则（透支/转账/过期）拍板
- 统一状态机与错误码定义

**交付物**：领域模型文档、状态机文档、错误码表

### Phase 1：核心账务闭环（V1 核心）
- 商户、客户、账户、绑定关系、交易主单/明细/流水表落地
- 商户开户自动创建预算+收款账户
- 扣减/发放/转账/退款 API + 命令执行
- 入站幂等 + 执行幂等
- 基础补偿任务

**验收标准**：
- 并发压测下余额不穿透、无重复记账
- 商户开户后账户绑定关系完整且可追溯
- 失败重试后最终状态可收敛

### Phase 2：过期账本能力增强
- `support_expiry=true` 账户启用一级账本拆分（每个 `expire_at` 一个账本）
- `support_expiry=false` 账户维持“仅账户记账”路径
- 按有效期策略进行扣减顺序（如先到期先扣）
- 账本级对账与补偿策略增强

**验收标准**：
- 过期账户账本扣减顺序正确
- 非过期账户全链路不落账本
- 账本级流水与账户余额可对平

### Phase 3：通知与运营治理
- Outbox + 通知重试 + 死信治理
- 完善监控告警仪表盘
- 对账工具（按日核对 txn 与账户/账本余额变更）
- 灰度发布与回滚方案

---

## 10. 风险与应对

1. **高并发热点账户锁竞争**
   - 应对：账户分片、请求排队、热点限流、缩小事务范围

2. **重复请求穿透幂等**
   - 应对：应用层幂等 + DB 唯一键双保险

3. **外部通知不稳定**
   - 应对：Outbox、重试退避、死信人工兜底

4. **状态机分叉导致脏状态**
   - 应对：统一状态机组件 + 状态更新审计

5. **账户特性策略配置错误**
   - 应对：开户模板校验 + 配置变更审计 + 灰度发布

6. **金额精度问题**
   - 应对：全链路整数金额（分/厘），统一货币单位

---

## 11. 已确认决策（本轮）

1. V1 引入 `ledger_book`，仅用于 `support_expiry=true` 账户（按 `expire_at` 一层切分）。
2. 对外接口采用 HTTP。
3. 通知目标采用 Webhook。
4. V1 不保留 `SUSPEND` 场景。

## 12. 待确认决策点（剩余）

1. 幂等 TTL 是否按业务类型配置（推荐）？
2. 账户透支默认策略：默认关闭，还是按账户场景模板开启？
3. 过期账户的扣减策略是否采用“先到期先扣”（FEFO）？

---

## 13. 下一步执行建议

按本方案先做 **Phase 0 设计评审**。评审通过后我可以继续输出：
1. MySQL DDL 初稿（含 merchant/customer/account/ledger/txn）
2. Go 接口定义（application/domain/repo）
3. 账户特性校验器 + 状态机伪代码
4. 第一批集成测试用例清单（含透支/转账限制/过期账本）
