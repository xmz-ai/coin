# COIN V1 测试用例清单（TDD）

> 范围基于：`docs/handoff.md`、`docs/API.md`、`docs/DDL.md`、`docs/detailed-design.md`、`todo.md`
>
> 约定：
> - 金额单位均为最小货币单位（BIGINT）
> - 时间统一 UTC
> - 所有“冲突”均需断言错误码与无副作用（余额/流水不变）

---

## 0. 测试基线

### TC-0001 测试数据工厂可用
- **Given** 已初始化测试数据库与基础 fixture
- **When** 调用 merchant/customer/account/txn 工厂
- **Then** 数据创建成功且满足主外键约束

### TC-0002 可控时钟与 UUID 注入可用
- **Given** 测试环境注入固定 clock 与 uuid provider
- **When** 创建交易并查询
- **Then** created_at 与主键可预测、可断言

### TC-0003 最小回归脚本可执行
- **Given** 本地测试脚本已配置
- **When** 执行最小回归集
- **Then** 返回码为 0，报告包含模块级结果

---

## 1. 鉴权与防重放

### TC-1001 签名正确通过
- **Given** merchant_secret 正确、签名串正确
- **When** 发起请求
- **Then** 返回业务处理结果（非鉴权错误）

### TC-1002 签名错误拒绝
- **Given** merchant_secret 错误或签名串被篡改
- **When** 发起请求
- **Then** 返回签名错误码

### TC-1003 时间窗超限拒绝
- **Given** timestamp 超出允许窗口
- **When** 发起请求
- **Then** 返回重放/超时错误码

### TC-1004 nonce 可重复使用
- **Given** 首次请求已使用某 nonce 且签名正确
- **When** 在窗口内重复使用相同 nonce 再次请求
- **Then** 鉴权仍通过（不因 nonce 去重而拒绝）

### TC-1005 缺失鉴权头拒绝
- **Given** 缺失 `X-Merchant-No` 或 `X-Signature`
- **When** 发起请求
- **Then** 返回参数错误或鉴权错误码

---

## 2. 商户/客户/账户基础能力

### TC-2001 商户开户事务一致
- **Given** 有效创建商户请求
- **When** 调用商户创建
- **Then** 同事务创建 merchant + budget account + receivable account + binding

### TC-2002 商户外码唯一
- **Given** 已存在 merchant_no
- **When** 再次创建同 merchant_no
- **Then** 返回唯一约束冲突错误

### TC-2003 创建客户成功
- **Given** 有效 merchant 与 out_user_id
- **When** 创建客户
- **Then** customer 创建成功并归属 merchant

### TC-2004 客户唯一键冲突
- **Given** 同 merchant 下已存在 out_user_id
- **When** 再次创建
- **Then** 返回唯一约束冲突错误

### TC-2005 按 out_user_id 查询客户
- **Given** 客户已存在
- **When** 按 merchant + out_user_id 查询
- **Then** 返回正确 customer

---

## 3. 账户定位与幂等

### TC-3001 同 key 重复请求统一拒绝
- **Given** `(merchant_no, out_trade_no)` 首次请求成功
- **When** 再次提交同 `out_trade_no`
- **Then** 返回 HTTP 409 + `DUPLICATE_OUT_TRADE_NO`

### TC-3002 重复请求无副作用
- **Given** 首次请求已落账
- **When** 使用同 `out_trade_no` 重复提交（不论请求体是否变化）
- **Then** 余额、流水、交易状态均不发生新增或推进

### TC-3003 同侧双参一致放行
- **Given** `*_account_no` 与 `*_out_user_id` 最终解析同一账户
- **When** 发起请求
- **Then** 正常处理通过

### TC-3004 同侧双参不一致冲突
- **Given** `*_account_no` 与 `*_out_user_id` 解析不同账户
- **When** 发起请求
- **Then** 返回 `ACCOUNT_RESOLVE_CONFLICT`

### TC-3005 out_user_id 不用于商户系统账户定位
- **Given** 仅提供用于商户系统账户的 out_user_id
- **When** 发起需商户系统账户的交易
- **Then** 返回账户定位失败或参数错误

### TC-3006 processing_key 防并发重复执行
- **Given** 同 txn_no+stage 并发执行
- **When** 同时进入执行阶段
- **Then** 仅一个成功执行，其余命中执行幂等保护

---

## 4. TRANSFER 主链路（ISSUE/CONSUME/P2P）

### TC-4001 ISSUE 默认借方预算账户
- **Given** 未显式传 debit_account_no
- **When** 发起 ISSUE
- **Then** 借方账户为 merchant budget_account_no

### TC-4002 ISSUE 显式借方覆盖默认
- **Given** 显式传 debit_account_no
- **When** 发起 ISSUE
- **Then** 使用显式借方，不使用默认预算账户

### TC-4003 CONSUME 默认贷方收款账户
- **Given** 未显式传 credit_account_no
- **When** 发起 CONSUME
- **Then** 贷方账户为 merchant receivable_account_no

### TC-4004 CONSUME 显式贷方覆盖默认
- **Given** 显式传 credit_account_no
- **When** 发起 CONSUME
- **Then** 使用显式贷方，不使用默认收款账户

### TC-4005 P2P 不允许默认兜底
- **Given** P2P 请求缺少必要 from/to 定位
- **When** 发起请求
- **Then** 返回参数错误或定位失败

### TC-4006 能力约束：禁出拒绝
- **Given** 借方账户 `allow_debit_out=false`
- **When** 发起需借方扣减的交易
- **Then** 返回能力校验错误

### TC-4007 能力约束：禁入拒绝
- **Given** 贷方账户 `allow_credit_in=false`
- **When** 发起入账
- **Then** 返回能力校验错误

### TC-4008 能力约束：禁转拒绝
- **Given** 场景要求可转移但账户 `allow_transfer=false`
- **When** 发起交易
- **Then** 返回能力校验错误

### TC-4009 状态机合法迁移
- **Given** 交易在 INIT
- **When** 按流程推进
- **Then** 仅允许 `INIT->PROCESSING->PAY_SUCCESS->RECV_SUCCESS`

### TC-4010 状态机非法迁移拒绝
- **Given** 交易在 INIT
- **When** 直接更新为 RECV_SUCCESS
- **Then** 返回 `TXN_STATUS_INVALID`

### TC-4011 事务原子性
- **Given** 交易处理中在写流水阶段故障
- **When** 事务回滚
- **Then** 余额、主单、明细、流水、outbox 均不落部分结果

---

## 5. 账本模式路径（book_enabled，V1=过期维度）

### TC-5001 非过期账户禁止写账本
- **Given** `book_enabled=false`
- **When** 尝试写 `account_book` 或 `account_book_change_log`
- **Then** 请求失败

### TC-5002 FEFO 扣减顺序正确
- **Given** 同账户多个有效账本，过期时间不同
- **When** 扣减金额跨账本
- **Then** 先扣最早过期账本（FEFO）

### TC-5003 过期边界严格大于 now
- **Given** `expire_at == now_utc` 与 `expire_at > now_utc` 两类账本
- **When** 扣减
- **Then** 仅后者可参与

### TC-5004 过期账户入账必须提供 expire_at
- **Given** `book_enabled=true`
- **When** 入账请求缺少 expire_at
- **Then** 返回参数错误

### TC-5005 汇总一致性
- **Given** 过期账户发生多次入账与扣减
- **When** 执行一致性校验
- **Then** `account.balance == sum(account_book.balance)`

---

## 6. 退款链路

### TC-6001 原单不存在拒绝
- **Given** `refund_of_txn_no` 不存在
- **When** 发起退款
- **Then** 返回原单不存在错误

### TC-6002 超可退金额拒绝
- **Given** 原单 `refundable_amount < amount`
- **When** 发起退款
- **Then** 返回超退错误

### TC-6005 并发退款不超退
- **Given** 两个并发退款请求总额大于可退金额
- **When** 同时提交
- **Then** 至多一个成功，整体不超退

### TC-6006 退款反向记账正确
- **Given** 原交易成功
- **When** 发起合法退款
- **Then** 账户余额与流水方向与原交易相反且一致

---

## 7. 查询与分页

### TC-7001 按 txn_no 查询
- **Given** 交易已存在
- **When** 按 txn_no 查询
- **Then** 返回主单、明细、状态、可退金额

### TC-7002 按 out_trade_no 查询
- **Given** 交易已存在
- **When** 按 out_trade_no 查询
- **Then** 返回对应交易

### TC-7003 列表筛选条件生效
- **Given** 存在跨时间/状态/场景/用户维度交易
- **When** 分别组合查询
- **Then** 返回集合满足过滤条件

### TC-7004 seek 分页不重不漏
- **Given** 连续读取多页
- **When** 使用 page_token 翻页
- **Then** 无重复、无漏项、顺序稳定

### TC-7005 并发新增下分页稳定
- **Given** 翻页过程中有新交易写入
- **When** 继续按 token 翻页
- **Then** 已翻页窗口不回流，后续页稳定

---

## 8. Outbox / Webhook / 补偿

### TC-8001 主交易与 outbox 同事务
- **Given** 交易成功
- **When** 提交事务
- **Then** 必有对应 outbox_event

### TC-8002 Webhook 成功投递
- **Given** 回调端返回 2xx
- **When** worker 投递
- **Then** notify_log 标记 SUCCESS

### TC-8003 Webhook 失败重试
- **Given** 回调端持续失败
- **When** worker 多次重试
- **Then** retry_count 递增并按退避调度

### TC-8004 Webhook DEAD 收敛
- **Given** 超过最大重试次数
- **When** 继续调度
- **Then** 状态进入 DEAD，不再无限重试

### TC-8005 交易补偿推进滞留状态
- **Given** 存在 `PROCESSING/PAY_SUCCESS` 滞留交易
- **When** 执行补偿任务
- **Then** 交易推进到可收敛终态

---

## 9. 一致性与治理

### TC-9001 余额与流水一致
- **Given** 完整交易序列
- **When** 执行对账任务
- **Then** `account.balance` 与 account_change_log 聚合一致

### TC-9002 过期账户余额一致
- **Given** 过期账户交易序列
- **When** 执行对账任务
- **Then** 账户余额与账本聚合一致

### TC-9003 敏感信息不落日志
- **Given** 含 secret 的请求链路
- **When** 产生业务日志
- **Then** 日志不出现明文 `merchant_secret`

---

## 10. 回归套件分层（每次迭代必须执行）

### S0（快速冒烟，必跑）
- TC-1001, 1002, 1004
- TC-3001, 3002
- TC-4001, 4003
- TC-6002, 6005
- TC-7004
- TC-8001

### S1（模块回归）
- 当前迭代模块全部用例 + S0

### S2（发布前全量）
- 全部 TC-xxxx

---

## 11. 缺陷回归规则

- [ ] 每个线上/联调缺陷必须新增至少 1 条可自动化回归用例
- [ ] 缺陷修复 PR 必须附：失败用例截图（修复前）+ 通过截图（修复后）
- [ ] 缺陷用例纳入 S0/S1/S2 的分层策略并标注来源

---

## 12. 迭代与测试用例映射矩阵（todo 对齐）

### 迭代 0：测试基线与约束固化
- **必须覆盖**：TC-0001, TC-0002, TC-0003
- **回归要求**：S0 能在空骨架稳定执行

### 迭代 1：鉴权与防重放
- **新增覆盖**：TC-1001 ~ TC-1005
- **回归要求**：S1(鉴权) + S0

### 迭代 2：商户/客户/账户基础能力
- **新增覆盖**：TC-2001 ~ TC-2005
- **回归要求**：S1(账户域) + 迭代1 S1 + S0

### 迭代 3：账户定位与请求幂等
- **新增覆盖**：TC-3001 ~ TC-3006
- **回归要求**：S1(幂等/定位) + 迭代1~2关键回归 + S0

### 迭代 4：TRANSFER 主链路
- **新增覆盖**：TC-4001 ~ TC-4011
- **回归要求**：S1(主交易) + 迭代1~3关键回归 + S0

### 迭代 5：过期账本路径
- **新增覆盖**：TC-5001 ~ TC-5005
- **回归要求**：S1(账本) + 迭代1~4关键回归 + S0

### 迭代 6：退款链路
- **新增覆盖**：TC-6001 ~ TC-6006
- **回归要求**：S1(退款) + 迭代1~5关键回归 + S0

### 迭代 7：查询与稳定分页
- **新增覆盖**：TC-7001 ~ TC-7005
- **回归要求**：S1(查询) + 迭代1~6关键回归 + S0

### 迭代 8：Outbox / Webhook / 补偿
- **新增覆盖**：TC-8001 ~ TC-8005
- **回归要求**：S1(异步链路) + 迭代1~7关键回归 + S0

### 迭代 9：上线前回归与验收
- **新增覆盖**：TC-9001 ~ TC-9003
- **回归要求**：S2（全量 TC-xxxx）

### 迭代完成统一门禁（DoD）
- [ ] 当前迭代新增 TC 全部通过
- [ ] S0 全部通过
- [ ] 历史高风险用例（幂等冲突、并发退款、seek 分页）全部通过
- [ ] 无新增 P1/P0 缺陷遗留
