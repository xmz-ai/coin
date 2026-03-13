# Credits Ledger - API 设计（HTTP）

> 与 `plan.md` / `domain.md` / `DDL.md` 对齐。
> - 租户键：`merchant_id`
> - 请求幂等：`merchant_id + out_trade_no`
> - 鉴权：`merchant_id + merchant_secret` 签名
> - Customer 必须归属 Merchant

---

## 1. 通用约定

## 1.1 Base URL
- 采用统一前缀：`/api/v1`
- `txn_no/event_id/book_no` 返回值为**纯 UUIDv7**，不带 `txn_`/`evt_`/`book_` 前缀

## 1.2 Header
- `X-Merchant-Id`: 商户内部ID（UUIDv7，必填）
- `X-Timestamp`: 毫秒时间戳（必填）
- `X-Nonce`: 随机串（必填）
- `X-Signature`: 签名值（必填）
- `Content-Type: application/json`

## 1.3 签名规则（建议）

签名明文：
`METHOD + "\n" + PATH + "\n" + X-Merchant-Id + "\n" + X-Timestamp + "\n" + X-Nonce + "\n" + SHA256(body)`

签名算法：
- `HMAC-SHA256(secret, signing_string)`
- 结果 hex 小写

安全要求：
1. `X-Timestamp` 与服务端时间差不超过 5 分钟。
2. `X-Nonce` 在时间窗口内不可重放（Redis 去重）。
3. `merchant_secret` 仅商户侧持有；服务端存可解密密文（`secret_ciphertext`），验签时使用 `key_provider=LOCAL` + `kms_key_id=local_v1` 解密后参与 HMAC 计算。

## 1.4 响应结构

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

## 1.5 错误码（核心）
- `SUCCESS`
- `INVALID_PARAM`
- `INVALID_SIGNATURE`
- `MERCHANT_NOT_FOUND`
- `MERCHANT_DISABLED`
- `CUSTOMER_NOT_FOUND`
- `CUSTOMER_NOT_BELONG_TO_MERCHANT`
- `ACCOUNT_NOT_FOUND`
- `ACCOUNT_DISABLED`
- `ACCOUNT_FORBID_DEBIT`
- `ACCOUNT_FORBID_CREDIT`
- `ACCOUNT_FORBID_TRANSFER`
- `INSUFFICIENT_BALANCE`
- `IDEMPOTENT_CONFLICT`
- `TXN_NOT_FOUND`
- `REFUND_AMOUNT_EXCEEDED`
- `TXN_STATUS_INVALID`
- `INTERNAL_ERROR`

---

## 2. 商户与客户接口

## 2.1 创建商户（开户）

- `POST /api/v1/merchants`

Request:

```json
{
  "name": "Demo Merchant"
}
```

Response:

```json
{
  "code": "SUCCESS",
  "message": "ok",
  "data": {
    "merchant_id": "01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
    "merchant_no": "1000123456789012",
    "merchant_secret": "msk_xxx_only_once",
    "budget_account_no": "6217701201001234567",
    "receivable_account_no": "6217701202001234566"
  }
}
```

规则：
1. `merchant_secret` 仅返回一次。
2. 同事务创建商户 + 预算账户 + 收款账户 + 绑定关系。

## 2.2 轮转商户密钥

- `POST /api/v1/merchants/{merchant_id}/secret:rotate`

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
    "merchant_id": "01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
    "merchant_secret": "msk_new_only_once",
    "secret_version": 2
  }
}
```

## 2.3 创建客户

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
    "customer_id": "01956f4e-8c11-71aa-b2d2-2b079f7e1001",
    "customer_no": "2000123456789018",
    "merchant_id": "01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
    "merchant_no": "1000123456789012",
    "out_user_id": "u_90001"
  }
}
```

说明：
- `merchant_id` 从鉴权上下文获取，不接受 body 覆盖。
- `out_user_id` 在商户内唯一（`merchant_id + out_user_id`）。

---

## 3. 账户接口

## 3.1 创建账户

- `POST /api/v1/accounts`

Request:

```json
{
  "owner_type": "CUSTOMER",
  "owner_id": "01956f4e-8c11-71aa-b2d2-2b079f7e1001",
  "account_scene": "CUSTOM",
  "currency": "CREDIT",
  "capability": {
    "allow_overdraft": false,
    "max_overdraft_limit": 0,
    "allow_transfer": true,
    "allow_credit_in": true,
    "allow_debit_out": true,
    "support_expiry": false
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
1. `owner_type=CUSTOMER` 时，`owner_id` 必须归属当前商户。
2. `allow_overdraft=true && max_overdraft_limit=0` 表示无限透支。
3. `max_overdraft_limit < 0` 非法。

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
    "support_expiry": true,
    "book_balance_sum": 120000
  }
}
```

---

## 4. 交易接口

## 4.1 发放（Issue，TRANSFER 场景）

- `POST /api/v1/transactions/credit`

Request:

```json
{
  "out_trade_no": "ord_90001",
  "biz_type": "TRANSFER",
  "transfer_scene": "ISSUE",
  "debit_account_no": "6217701202001234566",
  "credit_account_no": "6217701201001234567",
  "amount": 1000,
  "expire_at": "2026-12-31T23:59:59.000Z"
}
```

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
    "status": "RECV_SUCCESS"
  }
}
```

校验：
1. 幂等键：`merchant_id + out_trade_no`
2. 借方需 `allow_debit_out=true`
3. 贷方需 `allow_credit_in=true`
4. 若贷方 `support_expiry=true`，可使用/创建 `account_book`

## 4.2 扣减（Consume，TRANSFER 场景）

- `POST /api/v1/transactions/debit`

Request:

```json
{
  "out_trade_no": "ord_90002",
  "biz_type": "TRANSFER",
  "transfer_scene": "CONSUME",
  "debit_account_no": "6217701201001234567",
  "amount": 300
}
```

Response:

```json
{
  "code": "SUCCESS",
  "data": {
    "txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2002",
    "status": "RECV_SUCCESS"
  }
}
```

校验：
- `allow_debit_out=true`
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
  "from_account_no": "6217701201001234567",
  "to_account_no": "6217701202001234566",
  "amount": 500,
  "to_expire_at": "2026-12-31T23:59:59.000Z"
}
```

校验：
1. `from.allow_transfer=true`
2. `from.allow_debit_out=true`
3. `to.allow_credit_in=true`
4. 币种一致
5. 转出转入同事务

## 4.4 退款（Refund）

- `POST /api/v1/transactions/refund`

Request:

```json
{
  "out_trade_no": "ord_90004_refund_1",
  "biz_type": "REFUND",
  "refund_of_txn_no": "01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
  "amount": 200,
  "refund_breakdown": [
    {
      "account_no": "6217701201001234567",
      "amount": 200
    }
  ]
}
```

校验：
1. 原单存在且可退
2. `amount <= origin.refundable_amount`
3. 并发退款通过 CAS 保证不超退

## 4.5 查询交易

- `GET /api/v1/transactions/{txn_no}`
- `GET /api/v1/transactions/by-out-trade-no/{out_trade_no}`

返回主单状态、明细、错误码、可退余额。

---

## 5. 幂等与重试语义

## 5.1 幂等语义
- Key: `(merchant_id, out_trade_no)`
- 相同请求体重复提交：返回首次成功结果
- 相同 key 但请求体关键字段不一致：返回 `IDEMPOTENT_CONFLICT`

## 5.2 客户端超时重试建议
1. 先用相同 `out_trade_no` 重试原接口。
2. 或走“按 out_trade_no 查询”确认状态。
3. 禁止客户端随机更换 `out_trade_no` 盲重试。

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
  "merchant_id": "01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
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
- 签名算法同 API，secret 使用商户 webhook secret（建议与 API secret 分离）

## 6.4 重试策略
- 指数退避（如 1m/5m/15m/1h/6h）
- 达上限后标记 DEAD，进入人工处理队列

---

## 7. 状态机与事务边界

## 7.1 状态机
- `INIT -> PROCESSING -> PAY_SUCCESS -> RECV_SUCCESS`
- 任一步异常：`-> FAILED`

## 7.2 事务边界
- 单笔交易内：主单、明细、余额、流水、outbox 同事务。
- Webhook 投递异步执行，不阻塞主交易提交。

---

## 8. API 与 DDL 字段映射要点

1. `merchant_id` 从鉴权上下文注入到 `txn.merchant_id`。
2. `out_trade_no` 写入 `txn.out_trade_no` 并受唯一键保护。
3. `support_expiry=true` 时，detail 可落 `debit_book_no/credit_book_no`。
4. 所有金额字段统一 int64（最小货币单位）。

---

## 9. 最小测试矩阵（接口层）

1. 鉴权失败：签名错误、时间戳过期、nonce 重放。
2. 幂等：重复请求一致返回；冲突请求报 `IDEMPOTENT_CONFLICT`。
3. 能力约束：
   - `allow_credit_in=false` 不能入账
   - `allow_debit_out=false` 不能出账
   - `allow_transfer=false` 不能转出
4. 透支语义：
   - 有限透支成功/失败边界
   - 无限透支（limit=0）可通过
5. 过期路径：
   - `support_expiry=false` 不落账本
   - `support_expiry=true` 落账本并与账户汇总对齐
6. 退款并发：多并发退款不超原单可退金额。
