# coin merchant sdk (go)

面向商户 App 的 Go SDK，封装 coin HTTP API 的商户业务能力。

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
- `GET /api/v1/accounts/{account_no}/change-logs`

不支持（平台后台能力）：
- `POST /api/v1/merchants`
- `POST /api/v1/merchants/{merchant_no}/secret:rotate`
- `PUT /api/v1/webhooks/config`

## Install

```bash
go get github.com/xmz-ai/coin/sdk/go/coin
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/xmz-ai/coin/sdk/go/coin"
)

func main() {
	client, err := coin.NewClient(coin.ClientOptions{
		BaseURL:        "https://api.example.com",
		MerchantNo:     "1000123456789012",
		MerchantSecret: "msk_xxx",
	})
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.Transactions.Credit(context.Background(), coin.CreditRequest{
		OutTradeNo: "ord_issue_001",
		Title:      "积分发放",
		Remark:     "活动首单赠送",
		UserID:     "u_90001",
		Amount:     1000,
	})
	if err != nil {
		if apiErr, ok := err.(*coin.APIError); ok {
			log.Fatalf("api failed: code=%s request_id=%s", apiErr.Code, apiErr.RequestID)
		}
		log.Fatal(err)
	}

	fmt.Printf("txn=%s status=%s\n", resp.TxnNo, resp.Status)
}
```

## Notes

- SDK 只提供原子接口，不内置自动轮询或业务重试。
- 所有请求自动签名（HMAC-SHA256），签名串和服务端一致。
- 错误统一返回 `*APIError`（包含 HTTP 状态、业务错误码、request_id）。
