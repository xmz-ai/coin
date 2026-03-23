# coin merchant sdk (typescript)

面向商户 App 的 TypeScript SDK，封装 coin HTTP API 的商户业务能力。

## Scope (v1)

支持：
- `GET /api/v1/merchants/me`
- `POST /api/v1/transactions/credit`
- `POST /api/v1/transactions/debit`
- `POST /api/v1/transactions/transfer`
- `POST /api/v1/transactions/refund`
- `GET /api/v1/transactions/{txn_no}`
- `GET /api/v1/transactions/by-out-trade-no/{out_trade_no}`
- `GET /api/v1/transactions`

不支持（平台后台能力）：
- `POST /api/v1/merchants`
- `POST /api/v1/merchants/{merchant_no}/secret:rotate`
- `PUT /api/v1/webhooks/config`

## Install

```bash
npm install @xmz-ai/coin
```

## Quick Start

```typescript
import { CoinClient, CoinAPIError } from "@xmz-ai/coin";

const client = new CoinClient({
  baseURL: "https://api.example.com",
  merchantNo: "1000123456789012",
  merchantSecret: "msk_xxx",
});

const resp = await client.transactions.credit({
  out_trade_no: "ord_issue_001",
  user_id: "u_90001",
  amount: 1000,
});

console.log(`txn=${resp.txn_no} status=${resp.status}`);
```

## Error Handling

```typescript
try {
  await client.transactions.credit({ ... });
} catch (err) {
  if (err instanceof CoinAPIError) {
    console.error(`api failed: code=${err.code} request_id=${err.requestId}`);
  }
  throw err;
}
```

## Notes

- SDK 只提供原子接口，不内置自动轮询或业务重试。
- 所有请求自动签名（HMAC-SHA256），签名串和服务端一致。
- 错误统一返回 `CoinAPIError`（包含 HTTP 状态、业务错误码、request_id）。
- 需要 Node.js >= 18（使用 `node:crypto` 和全局 `fetch`）。
