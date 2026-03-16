package integration

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/api"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
	"github.com/xmz-ai/coin/tests/support/memoryrepo"
)

const (
	benchDebitAccountNo  = "6217701201999900001"
	benchCreditAccountNo = "6217701201999900002"
)

type noopAsyncDispatcher struct{}

func (noopAsyncDispatcher) Enqueue(string) {}

func BenchmarkCoreTxnTransferSubmitAPIMemory(b *testing.B) {
	router, merchantNo, secret := newCoreTxnBenchmarkServer(b)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := signedBenchmarkAPIRequest(
			b,
			http.MethodPost,
			"/api/v1/transactions/transfer",
			merchantNo,
			secret,
			"bench-nonce-api-"+strconv.Itoa(i),
			map[string]any{
				"out_trade_no":    "ord_bench_api_" + strconv.Itoa(i),
				"transfer_scene":  "P2P",
				"from_account_no": benchDebitAccountNo,
				"to_account_no":   benchCreditAccountNo,
				"amount":          1,
			},
		)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusCreated {
			b.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
		}
	}
}

func BenchmarkCoreTxnTransferFullPathServiceMemory(b *testing.B) {
	repo, merchantNo, transferSvc, processor := newCoreTxnBenchmarkServices(b)
	_, _ = repo, merchantNo

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		txn, err := transferSvc.Submit(service.TransferRequest{
			MerchantNo:       merchantNo,
			OutTradeNo:       "ord_bench_full_" + strconv.Itoa(i),
			BizType:          service.BizTypeTransfer,
			TransferScene:    service.SceneP2P,
			DebitAccountNo:   benchDebitAccountNo,
			CreditAccountNo:  benchCreditAccountNo,
			Amount:           1,
			RefundableAmount: 1,
			Status:           service.TxnStatusInit,
		})
		if err != nil {
			b.Fatalf("submit failed: %v", err)
		}
		if err := processor.Process(txn.TxnNo); err != nil {
			b.Fatalf("process failed: %v", err)
		}
	}
}

func newCoreTxnBenchmarkServer(b *testing.B) (*gin.Engine, string, string) {
	b.Helper()
	gin.SetMode(gin.TestMode)

	repo, merchantNo, transferSvc, _ := newCoreTxnBenchmarkServices(b)
	transferRoutingSvc := service.NewTransferRoutingService(repo)
	accountResolver := service.NewAccountResolver(repo)
	refundSvc := service.NewRefundService(repo)
	querySvc := service.NewTxnQueryService(repo)

	base := time.Unix(1_710_000_000, 0).UTC()
	secret := "sec_core_txn_bench"
	authMw := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: api.StaticMerchantSecretProvider{merchantNo: secret},
		NowFn:          func() time.Time { return base },
		TimeWindow:     5 * time.Minute,
	})

	business := api.NewBusinessHandler(
		transferSvc,
		transferSvc,
		repo,
		transferRoutingSvc,
		noopAsyncDispatcher{},
		noopAsyncDispatcher{},
		accountResolver,
		repo,
		refundSvc,
		querySvc,
		repo,
		func() time.Time { return base },
	)

	r := api.NewRouter()
	api.RegisterProtectedRoutes(r, api.ProtectedRoutesOptions{
		AuthMiddleware: authMw,
		Business:       business,
	})
	return r, merchantNo, secret
}

func newCoreTxnBenchmarkServices(b *testing.B) (*memoryrepo.Repo, string, *service.TransferService, *service.TransferAsyncProcessor) {
	b.Helper()

	repo := memoryrepo.New()
	ids := idpkg.NewRuntimeUUIDProvider()
	merchantSvc := service.NewMerchantService(repo, ids)
	merchant, err := merchantSvc.CreateMerchant("", "core-txn-bench")
	if err != nil {
		b.Fatalf("create merchant failed: %v", err)
	}

	if err := repo.CreateAccount(service.Account{
		AccountNo:         benchDebitAccountNo,
		MerchantNo:        merchant.MerchantNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    false,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		Balance:           1_000_000_000_000_000,
	}); err != nil {
		b.Fatalf("create debit account failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:         benchCreditAccountNo,
		MerchantNo:        merchant.MerchantNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    false,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		Balance:           0,
	}); err != nil {
		b.Fatalf("create credit account failed: %v", err)
	}

	transferSvc := service.NewTransferService(repo, ids)
	processor := service.NewTransferAsyncProcessor(repo)
	return repo, merchant.MerchantNo, transferSvc, processor
}

func signedBenchmarkAPIRequest(b *testing.B, method, rawURL, merchantNo, secret, nonce string, payload any) *http.Request {
	b.Helper()

	var bodyBytes []byte
	if payload != nil {
		v, err := json.Marshal(payload)
		if err != nil {
			b.Fatalf("marshal payload failed: %v", err)
		}
		bodyBytes = v
	} else {
		bodyBytes = []byte{}
	}

	ts := time.Unix(1_710_000_000, 0).UTC()
	tsText := strconv.FormatInt(ts.UnixMilli(), 10)
	bodyHash := sha256.Sum256(bodyBytes)
	signingString := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%s",
		method,
		rawURL,
		merchantNo,
		tsText,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingString))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(method, rawURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Merchant-No", merchantNo)
	req.Header.Set("X-Timestamp", tsText)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)
	return req
}
