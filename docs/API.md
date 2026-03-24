# Credits Ledger - API 设计（HTTP）

> 与 `DDL.md` / `CODE_RULES.md` / `requirements-design.md` 对齐。
> - 对外租户键：`merchant_no`
> - 请求幂等：`merchant_no + out_trade_no`
> - 鉴权：`merchant_no + merchant_secret` 签名
> - Customer 必须归属 Merchant

---

## 1. 通用约定

## 1.1 Base URL
- 采用统一前缀：`/api/v1`
- `txn_no/event_id/book_no` 返回值为**纯 UUIDv7**，不带 `txn_`/`evt_`/`book_` 前缀

## 1.2 Header
- `X-Merchant-No`: 商户外部编码（16位数字，必填）
- `X-Timestamp`: 毫秒时间戳（必填）
- `X-Nonce`: 随机串（必填）
- `X-Signature`: 签名值（必填）
- `Content-Type: application/json`

## 1.3 签名规则（建议）

签名明文：
`METHOD + "\n" + PATH + "\n" + X-Merchant-No + "\n" + X-Timestamp + "\n" + X-Nonce + "\n" + SHA256(body)`

签名算法：
- `HMAC-SHA256(secret, signing_string)`
- 结果 hex 小写

安全要求：
1. `X-Timestamp` 与服务端时间差不超过 5 分钟。
2. `X-Nonce` 参与签名计算，不做服务端去重校验。
3. `merchant_secret` 仅商户侧持有；服务端存可解密密文（`secret_ciphertext`），验签时使用 `key_provider=LOCAL` + `kms_key_id=LOCAL_KMS_KEY_V1` 解密后参与 HMAC 计算。

## 1.4 字段口径统一（重要）
- 对外鉴权与接口字段统一使用：`merchant_no`（Header: `X-Merchant-No`）。
- 除 `merchant` 主表外，业务关联字段统一使用：`merchant_no`。
- 签名串第三段固定为 `X-Merchant-No`，不得使用 `merchant_id`。

## 1.5 响应结构

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "request_id": "req_20260312_xxx",
  "data": {}
}
```

错误响应：

```json
{
  "code": "INVALID_SIGNATURE",
  "message": "signature verify failed",
  "request_id": "req_20260312_xxx"
}
```

## 1.6 错误码（核心）
- `SUCCESS`
- `INVALID_PARAM`
- `INVALID_SIGNATURE`
- `AUTH_HEADER_MISSING`
- `TIMESTAMP_OUT_OF_WINDOW`
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
- `REFUND_ORIGIN_NOT_FOUND`
- `REFUND_ORIGIN_INVALID`
- `REFUND_AMOUNT_EXCEEDED`
- `REFUND_ORIGIN_BOOK_TRACE_MISSING`
- `TXN_STATUS_INVALID`
- `INTERNAL_ERROR`

## 1.7 错误码与 HTTP 语义映射

| code | HTTP | retryable | client_action |
|---|---:|---|---|
| `SUCCESS` | 200 | 否 | 正常处理返回结果 |
| `INVALID_PARAM` | 400 | 否 | 修正请求参数后重试 |
| `INVALID_SIGNATURE` | 401 | 否 | 修正签名实现/密钥后重试 |
| `AUTH_HEADER_MISSING` | 400 | 否 | 补齐鉴权 Header 后重试 |
| `TIMESTAMP_OUT_OF_WINDOW` | 401 | 否 | 校准客户端时钟并重试 |
| `MERCHANT_NOT_FOUND` | 404 | 否 | 检查商户号配置 |
| `MERCHANT_DISABLED` | 403 | 否 | 联系平台恢复商户状态 |
| `CUSTOMER_NOT_FOUND` | 404 | 否 | 先创建客户或修正标识 |
| `CUSTOMER_NOT_BELONG_TO_MERCHANT` | 403 | 否 | 修正商户与客户归属关系 |
| `ACCOUNT_NOT_FOUND` | 404 | 否 | 检查账号或先开户 |
| `OUT_USER_ID_NOT_FOUND` | 404 | 否 | 修正 user_id 或先创建该用户对应账户 |
| `ACCOUNT_RESOLVE_CONFLICT` | 409 | 否 | 保留一种定位方式或修正为同一账户 |
| `ACCOUNT_DISABLED` | 403 | 否 | 更换可用账户或恢复账户状态 |
| `ACCOUNT_FORBID_DEBIT` | 403 | 否 | 更换允许出账账户或调整能力 |
| `ACCOUNT_FORBID_CREDIT` | 403 | 否 | 更换允许入账账户或调整能力 |
| `ACCOUNT_FORBID_TRANSFER` | 403 | 否 | 更换允许转账账户或调整能力 |
| `INSUFFICIENT_BALANCE` | 409 | 否 | 充值/调账后重试 |
| `DUPLICATE_OUT_TRADE_NO` | 409 | 否 | 重复下单不被支持，请查单或更换 `out_trade_no` |
| `TXN_NOT_FOUND` | 404 | 否 | 检查 `txn_no/out_trade_no` |
| `REFUND_ORIGIN_NOT_FOUND` | 409 | 否 | 确认原单是否存在且归属当前商户 |
| `REFUND_ORIGIN_INVALID` | 409 | 否 | 原单必须是 `TRANSFER` 且状态为 `RECV_SUCCESS` |
| `REFUND_AMOUNT_EXCEEDED` | 409 | 否 | 降低退款金额或查询可退余额 |
| `REFUND_ORIGIN_BOOK_TRACE_MISSING` | 409 | 否 | 检查原单账本分录是否完整并联系平台排查 |
| `TXN_STATUS_INVALID` | 409 | 否 | 按状态机允许路径操作 |
| `INTERNAL_ERROR` | 500 | 是 | 先按 `out_trade_no` 查单确认；确认需新交易时使用新 `out_trade_no` |

说明：
- 建议客户端仅在 `retryable=true` 或网络超时场景重试。
- V1 不支持使用同一 `out_trade_no` 重复下单。

---

## 2. 商户与客户接口

## 2.1 创建商户（开户）

- `POST /api/v1/merchants`

Request:

```json
{
  "name": "Demo Merchant",
  "auto_create_account_on_customer_create": true,
  "auto_create_customer_on_credit": true
}
```

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "merchant_no": "1000123456789012",
    "merchant_secret": "msk_xxx_only_once",
    "budget_account_no": "6217701201001234567",
    "receivable_account_no": "6217701202001234566",
    "auto_create_account_on_customer_create": true,
    "auto_create_customer_on_credit": true
  }
}
```

规则：
1. `merchant_secret` 仅返回一次。
2. 同事务创建商户 + 预算账户 + 收款账户 + 绑定关系。
3. `auto_create_account_on_customer_create` / `auto_create_customer_on_credit` 为可选开关，默认 `true`。

## 2.2 轮转商户密钥

- `POST /api/v1/merchants/{merchant_no}/secret:rotate`
- 生效规则：新密钥生效后旧密钥立即失效

Request:

```json
{
  "reason": "periodic rotation"
}
```

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "merchant_no": "1000123456789012",
    "merchant_secret": "msk_new_only_once",
    "secret_version": 2
  }
}
```

## 2.3 查询当前商户配置

- `GET /api/v1/merchants/me`

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "merchant_no": "1000123456789012",
    "name": "Demo Merchant",
    "status": "ACTIVE",
    "budget_account_no": "6217701201001234567",
    "receivable_account_no": "6217701202001234566",
    "secret_version": 2,
    "auto_create_account_on_customer_create": true,
    "auto_create_customer_on_credit": true
  }
}
```

说明：
- 仅返回当前鉴权商户配置。
- `secret_version` 在商户尚未轮转密钥时可能为 `0`。

## 2.4 创建客户

- `POST /api/v1/customers`

Request:

```json
{
  "out_user_id": "u_90001"
}
```

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "customer_no": "2000123456789018",
    "merchant_no": "1000123456789012",
    "out_user_id": "u_90001"
  }
}
```

说明：
- `merchant_no` 从鉴权上下文获取，不接受 body 覆盖。
- `out_user_id` 在商户内唯一（`merchant_no + out_user_id`）。

## 2.5 按 out_user_id 查询客户

- `GET /api/v1/customers/by-out-user-id/{out_user_id}`

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "customer_no": "2000123456789018",
    "merchant_no": "1000123456789012",
    "out_user_id": "u_90001",
    "status": "ACTIVE"
  }
}
```

说明：
- 仅允许查询当前鉴权商户下客户。

---

## 3. 账户接口

## 3.1 创建账户

- `POST /api/v1/accounts`

Request:

```json
{
  "owner_type": "CUSTOMER",
  "owner_out_user_id": "u_90001",
  "account_scene": "CUSTOM",
  "currency": "CREDIT",
  "capability": {
    "allow_overdraft": false,
    "max_overdraft_limit": 0,
    "allow_transfer": true,
    "allow_credit_in": true,
    "allow_debit_out": true,
    "book_enabled": false
  }
}
```

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "account_no": "6217701201001234567"
  }
}
```

校验：
1. `owner_type=CUSTOMER` 时，`owner_out_user_id` 或 `owner_customer_no` 二选一，且必须归属当前商户。
2. 同时传 `owner_out_user_id` 与 `owner_customer_no` 且解析不一致，返回 `ACCOUNT_RESOLVE_CONFLICT`。
3. `allow_overdraft=true && max_overdraft_limit=0` 表示无限透支。
4. `max_overdraft_limit < 0` 非法。
5. `book_enabled=true` 表示启用父账户+子账本模式（不是“已有账本数据”），V1 子账本仅按 `expire_at` 维度切分。
6. 以下字段创建后固定，不提供变更接口：`merchant_no`、`customer_no`、`account_type`、`allow_overdraft`、`max_overdraft_limit`、`book_enabled`。

## 3.2 更新账户能力

- `PATCH /api/v1/accounts/{account_no}/capability`

Request:

```json
{
  "allow_transfer": false,
  "allow_credit_in": true,
  "allow_debit_out": true
}
```

Response: `SUCCESS`

说明：
- 可部分更新。
- 本接口仅允许更新 `allow_transfer`、`allow_credit_in`、`allow_debit_out`。
- `allow_overdraft`、`max_overdraft_limit`、`book_enabled` 以及账户归属/类型字段均视为开户时确定，不支持运行时修改。
- 建议记录审计日志（谁在何时改了哪个能力）。

## 3.3 查询账户余额

- `GET /api/v1/accounts/{account_no}/balance`

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "account_no": "6217701201001234567",
    "balance": 120000,
    "book_enabled": true,
    "book_balance_sum": 120000
  }
}
```

---

## 4. 交易接口

账户定位通用规则：
1. `*_account_no` 与对应 `*_out_user_id` 可二选一。
2. `*_out_user_id` 仅用于定位商户下客户账户，不用于定位商户系统账户。
3. 同侧同时传两者时：若解析结果一致则允许通过；若解析不一致返回 `ACCOUNT_RESOLVE_CONFLICT`。
4. 发放接口（`/transactions/credit`）使用简化字段 `user_id` 表示入账用户。

## 4.1 发放（Issue，TRANSFER 场景）

- `POST /api/v1/transactions/credit`

Request:

```json
{
  "out_trade_no": "ord_90001",
  "user_id": "u_90001",
  "amount": 1000,
  "expire_in_days": 30
}
```

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
    "status": "INIT"
  }
}
```

说明：
- 校验通过时 HTTP 状态码为 `201 Created`
- 原单不存在/不归属当前商户、原单状态非法、退款金额超限会在提交阶段直接返回 `404/409`
- 建单成功即返回，服务端优先进程内异步执行；轮询 worker 仅用于故障恢复兜底

校验：
1. 幂等键：`merchant_no + out_trade_no`
2. `biz_type/transfer_scene` 由服务端固定写入 `TRANSFER/ISSUE`，请求无需传入
3. 账户定位（借方）：优先 `debit_account_no`，仍未提供则默认商户 `budget_account_no`
4. 账户定位（贷方）：优先 `credit_account_no`，否则 `user_id`（ISSUE 不提供贷方则报 `INVALID_PARAM`）
5. 同时传 `credit_account_no` 与 `user_id` 且解析不一致，返回 `ACCOUNT_RESOLVE_CONFLICT`
6. 借方需 `allow_debit_out=true`
7. 贷方需 `allow_credit_in=true`
8. 若贷方 `book_enabled=true`，`expire_in_days` 必填（`>0`），服务端按 `now_utc + expire_in_days` 计算到期时间
9. `user_id` 未匹配客户账户时返回 `OUT_USER_ID_NOT_FOUND`（语义：按 `user_id` 解析不到账户）

## 4.2 扣减（Consume，TRANSFER 场景）

- `POST /api/v1/transactions/debit`

Request:

```json
{
  "out_trade_no": "ord_90002",
  "biz_type": "TRANSFER",
  "transfer_scene": "CONSUME",
  "debit_out_user_id": "u_90001",
  "amount": 300
}
```

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2002",
    "status": "INIT"
  }
}
```

说明：
- HTTP 状态码为 `201 Created`
- 建单成功即返回，服务端优先进程内异步执行；轮询 worker 仅用于故障恢复兜底

校验：
- 账户定位（借方）：优先 `debit_account_no`，否则 `debit_out_user_id`（CONSUME 借方必填其一）
- 账户定位（贷方）：优先 `credit_account_no`，否则 `credit_out_user_id`，仍未提供则默认当前商户 `receivable_account_no`
- 同侧同时传 `*_account_no` 与 `*_out_user_id` 且解析不一致，返回 `ACCOUNT_RESOLVE_CONFLICT`
- `allow_debit_out=true`
- 显式或解析出的贷方需 `allow_credit_in=true`
- `*_out_user_id` 未匹配客户/默认账户时返回 `OUT_USER_ID_NOT_FOUND`
- 透支规则：
  - `allow_overdraft=false` => `balance >= amount`
  - `allow_overdraft=true && max_overdraft_limit>0` => `balance + limit >= amount`
  - `allow_overdraft=true && max_overdraft_limit=0` => 无限透支

## 4.3 转账（P2P，TRANSFER 场景）

- `POST /api/v1/transactions/transfer`

Request:

```json
{
  "out_trade_no": "ord_90003",
  "biz_type": "TRANSFER",
  "transfer_scene": "P2P",
  "from_out_user_id": "u_90001",
  "to_out_user_id": "u_90002",
  "amount": 500,
  "to_expire_in_days": 30
}
```

校验：
1. 账户定位：`from_account_no` 或 `from_out_user_id` 二选一；`to_account_no` 或 `to_out_user_id` 二选一
2. P2P 不走商户默认账户兜底，from/to 两侧必须可解析
3. 同侧同时传 `*_account_no` 与 `*_out_user_id` 且解析不一致，返回 `ACCOUNT_RESOLVE_CONFLICT`
4. `from.allow_transfer=true`
5. `from.allow_debit_out=true`
6. `to.allow_credit_in=true`
7. 若 `to.book_enabled=true`，`to_expire_in_days` 必填（`>0`），服务端按 `now_utc + to_expire_in_days` 计算到期时间
8. 币种一致
9. 转出转入同事务

说明：
- HTTP 状态码为 `201 Created`
- 建单成功即返回，服务端优先进程内异步执行；轮询 worker 仅用于故障恢复兜底

## 4.4 退款（Refund）

- `POST /api/v1/transactions/refund`

Request:

```json
{
  "out_trade_no": "ord_90004_refund_1",
  "biz_type": "REFUND",
  "refund_of_txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
  "amount": 200
}
```

校验：
1. 同步提交阶段：校验请求参数与幂等（`out_trade_no/refund_of_txn_no/amount`）。
2. 同步提交阶段：校验原单必须存在、归属当前商户，且 `biz_type=TRANSFER`、`status=RECV_SUCCESS`，并且 `amount <= origin.refundable_amount`。
3. 同步提交阶段不扣减原单可退余额；异步执行阶段仍通过 CAS 递减 `origin.refundable_amount`，保证并发不超退。

输出：
- 提交响应固定返回 `txn_no/status`（`status=INIT`）。
- 可退余额变化与失败原因通过查单获取（如 `REFUND_ORIGIN_NOT_FOUND/REFUND_ORIGIN_INVALID/REFUND_AMOUNT_EXCEEDED`）。

说明：
- HTTP 状态码为 `201 Created`
- 建单成功即返回，服务端优先进程内异步执行；轮询 worker 仅用于故障恢复兜底

## 4.5 查询交易

- `GET /api/v1/transactions/{txn_no}`
- `GET /api/v1/transactions/by-out-trade-no/{out_trade_no}`

返回主单状态、明细、错误码、可退余额。

## 4.6 交易列表查询

分页口径：
- 使用 seek 分页，稳定排序键为 `created_at DESC, txn_no DESC`。
- `page_token` 编码上一页最后一条记录的 `(created_at, txn_no)`。
- 下一页条件按同序比较：`(created_at, txn_no) < (:created_at, :txn_no)`。

- `GET /api/v1/transactions`

Query 参数（示例）：
- `start_time` / `end_time`（UTC）
- `status`（如 `RECV_SUCCESS/FAILED`）
- `transfer_scene`（`ISSUE/CONSUME/P2P`）
- `out_user_id`（可选，用于按客户维度过滤）
- `page_size`（默认 20，最大 200）
- `page_token`（游标，编码 `created_at + txn_no`）

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "items": [
      {
        "txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
        "out_trade_no": "ord_90001",
        "transfer_scene": "ISSUE",
        "amount": 1000,
        "status": "RECV_SUCCESS",
        "created_at": "2026-03-12T11:30:00.000Z"
      }
    ],
    "next_page_token": "eyJvZmZzZXQiOjIwfQ=="
  }
}
```

## 4.7 接入方最小请求示例

### 示例 A：最简发放（商户预算账户默认出账）

- 场景：商户给 `user_id=u_90001` 发放 1000 分。
- 要点：不传 `debit_account_no`，默认走商户 `budget_account_no`。

```json
{
  "out_trade_no": "ord_issue_min_001",
  "user_id": "u_90001",
  "amount": 1000
}
```

### 示例 B：最简扣减（商户收款账户默认入账）

- 场景：从 `out_user_id=u_90001` 扣减 300 分。
- 要点：不传 `credit_account_no`，默认入商户 `receivable_account_no`。

```json
{
  "out_trade_no": "ord_consume_min_001",
  "biz_type": "TRANSFER",
  "transfer_scene": "CONSUME",
  "debit_out_user_id": "u_90001",
  "amount": 300
}
```

### 示例 C：P2P 转账（用户到用户）

- 场景：`u_90001` 向 `u_90002` 转 500 分。
- 要点：P2P 不走商户默认账户，from/to 必须可解析。

```json
{
  "out_trade_no": "ord_p2p_min_001",
  "biz_type": "TRANSFER",
  "transfer_scene": "P2P",
  "from_out_user_id": "u_90001",
  "to_out_user_id": "u_90002",
  "amount": 500
}
```

---

## 5. 幂等与重试语义

## 5.1 幂等语义
- Key: `(merchant_no, out_trade_no)`
- 执行幂等键（`processing_key`）TTL：全局统一 TTL
- 同 key 后续重复请求：一律返回 HTTP 409（不支持重复下单）
- 重复请求拒绝时不得产生任何副作用（余额/流水/状态均不变）

## 5.2 重复请求处理规则

- 同一 `merchant_no` 下，若 `out_trade_no` 已存在：直接返回 HTTP 409。
- 不再区分“相同请求体重放”与“关键字段冲突”两种分支。
- 重复请求返回后，系统状态必须保持不变（无额外记账、无额外流水、无状态推进）。

## 5.3 客户端超时重试建议
1. 不要使用相同 `out_trade_no` 重试下单接口（会返回 409）。
2. 通过“按 out_trade_no 查询”确认原请求处理结果。
3. 仅在业务上确认需要新交易时，使用新的 `out_trade_no` 发起新请求。

---

## 6. Webhook 通知

## 6.1 回调事件
- `TxnSucceeded`
- `TxnFailed`
- `TxnRefunded`

## 6.2 通知载荷（示例）

```json
{
  "event_id": "01956f4e-ae33-75cd-90a2-4c6f9d8b3001",
  "event_type": "TxnSucceeded",
  "occurred_at": "2026-03-12T11:30:00.000Z",
  "merchant_no": "1000123456789012",
  "txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
  "out_trade_no": "ord_90001",
  "biz_type": "TRANSFER",
  "transfer_scene": "ISSUE",
  "amount": 1000,
  "status": "RECV_SUCCESS"
}
```

## 6.3 回调验签（建议）
- Header: `X-Event-Id`, `X-Signature`, `X-Timestamp`
- 签名算法同 API，V1 复用商户 API `merchant_secret`

## 6.4 Webhook 配置管理

### 6.4.1 查询配置
- `GET /api/v1/webhooks/config`

### 6.4.2 更新配置
- `PUT /api/v1/webhooks/config`

Request:

```json
{
  "url": "https://merchant.example.com/coin/webhook",
  "enabled": true,
  "retry_policy": {
    "max_retries": 8,
    "backoff": ["1m", "5m", "15m", "1h", "6h"]
  }
}
```

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "url": "https://merchant.example.com/coin/webhook",
    "enabled": true,
    "retry_policy": {
      "max_retries": 8,
      "backoff": ["1m", "5m", "15m", "1h", "6h"]
    }
  }
}
```

约束：
- 仅允许操作当前鉴权商户配置。
- `url` 必须为 `https`。

## 6.5 重试策略
- 指数退避（如 1m/5m/15m/1h/6h）
- 达上限后标记 DEAD，进入人工处理队列

---

## 7. 状态机与事务边界

## 7.1 状态机
- `INIT -> PAY_SUCCESS -> RECV_SUCCESS`
- 任一步异常：`-> FAILED`

## 7.2 事务边界
- 单笔交易内：主单、余额、流水、outbox 同事务。
- Webhook 投递异步执行，不阻塞主交易提交。

---

## 8. API 与 DDL 字段映射要点

1. `merchant_no` 从鉴权上下文解析后直接写入 `txn.merchant_no`。
2. `out_trade_no` 写入 `txn.out_trade_no` 并受唯一键保护。
3. `book_enabled=true` 时，book 路径记录在 `account_book` / `account_book_change_log`。
4. 所有金额字段统一 int64（最小货币单位）。

---

## 9. 最小测试矩阵（接口层）

1. 鉴权失败：签名错误、时间戳过期、鉴权头缺失。
2. 幂等：同 `out_trade_no` 重复请求统一返回 `DUPLICATE_OUT_TRADE_NO`（HTTP 409），且无副作用。
3. 能力约束：
   - `allow_credit_in=false` 不能入账
   - `allow_debit_out=false` 不能出账
   - `allow_transfer=false` 不能转出
4. 透支语义：
   - 有限透支成功/失败边界
   - 无限透支（limit=0）可通过
5. 过期路径：
   - `book_enabled=false` 不落账本
   - `book_enabled=true` 落账本并与账户汇总对齐
6. 退款并发：多并发退款不超原单可退金额。
