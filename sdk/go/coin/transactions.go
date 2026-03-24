package coin

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var outTradeNoPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// TransactionsAPI contains transaction submit and query APIs.
type TransactionsAPI struct {
	client *Client
}

func (t *TransactionsAPI) Credit(ctx context.Context, req CreditRequest) (TxnSubmitResponse, error) {
	var err error
	req.OutTradeNo, err = normalizeOutTradeNoAndAmount(req.OutTradeNo, req.Amount)
	if err != nil {
		return TxnSubmitResponse{}, err
	}
	if strings.TrimSpace(req.CreditAccountNo) == "" && strings.TrimSpace(req.UserID) == "" {
		return TxnSubmitResponse{}, fmt.Errorf("credit_account_no or user_id is required")
	}
	if req.ExpireInDays < 0 {
		return TxnSubmitResponse{}, fmt.Errorf("expire_in_days must be >= 0")
	}
	var out TxnSubmitResponse
	if err := t.client.do(ctx, "POST", "/api/v1/transactions/credit", nil, req, &out); err != nil {
		return TxnSubmitResponse{}, err
	}
	return out, nil
}

func (t *TransactionsAPI) Debit(ctx context.Context, req DebitRequest) (TxnSubmitResponse, error) {
	var err error
	req.OutTradeNo, err = normalizeOutTradeNoAndAmount(req.OutTradeNo, req.Amount)
	if err != nil {
		return TxnSubmitResponse{}, err
	}
	if strings.TrimSpace(req.DebitAccountNo) == "" && strings.TrimSpace(req.DebitOutUserID) == "" {
		return TxnSubmitResponse{}, fmt.Errorf("debit_account_no or debit_out_user_id is required")
	}
	var out TxnSubmitResponse
	if err := t.client.do(ctx, "POST", "/api/v1/transactions/debit", nil, req, &out); err != nil {
		return TxnSubmitResponse{}, err
	}
	return out, nil
}

func (t *TransactionsAPI) Transfer(ctx context.Context, req TransferRequest) (TxnSubmitResponse, error) {
	var err error
	req.OutTradeNo, err = normalizeOutTradeNoAndAmount(req.OutTradeNo, req.Amount)
	if err != nil {
		return TxnSubmitResponse{}, err
	}
	if strings.TrimSpace(req.FromAccountNo) == "" && strings.TrimSpace(req.FromOutUserID) == "" {
		return TxnSubmitResponse{}, fmt.Errorf("from_account_no or from_out_user_id is required")
	}
	if strings.TrimSpace(req.ToAccountNo) == "" && strings.TrimSpace(req.ToOutUserID) == "" {
		return TxnSubmitResponse{}, fmt.Errorf("to_account_no or to_out_user_id is required")
	}
	if req.ToExpireInDays < 0 {
		return TxnSubmitResponse{}, fmt.Errorf("to_expire_in_days must be >= 0")
	}
	var out TxnSubmitResponse
	if err := t.client.do(ctx, "POST", "/api/v1/transactions/transfer", nil, req, &out); err != nil {
		return TxnSubmitResponse{}, err
	}
	return out, nil
}

func (t *TransactionsAPI) Refund(ctx context.Context, req RefundRequest) (TxnSubmitResponse, error) {
	var err error
	req.OutTradeNo, err = normalizeOutTradeNoAndAmount(req.OutTradeNo, req.Amount)
	if err != nil {
		return TxnSubmitResponse{}, err
	}
	req.RefundOfTxnNo = strings.TrimSpace(req.RefundOfTxnNo)
	if req.RefundOfTxnNo == "" {
		return TxnSubmitResponse{}, fmt.Errorf("refund_of_txn_no is required")
	}
	var out TxnSubmitResponse
	if err := t.client.do(ctx, "POST", "/api/v1/transactions/refund", nil, req, &out); err != nil {
		return TxnSubmitResponse{}, err
	}
	return out, nil
}

func (t *TransactionsAPI) GetByTxnNo(ctx context.Context, txnNo string) (Txn, error) {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return Txn{}, fmt.Errorf("txn_no is required")
	}
	var out Txn
	if err := t.client.do(ctx, "GET", "/api/v1/transactions/"+url.PathEscape(txnNo), nil, nil, &out); err != nil {
		return Txn{}, err
	}
	return out, nil
}

func (t *TransactionsAPI) GetByOutTradeNo(ctx context.Context, outTradeNo string) (Txn, error) {
	outTradeNo = strings.TrimSpace(outTradeNo)
	if outTradeNo == "" {
		return Txn{}, fmt.Errorf("out_trade_no is required")
	}
	var out Txn
	if err := t.client.do(ctx, "GET", "/api/v1/transactions/by-out-trade-no/"+url.PathEscape(outTradeNo), nil, nil, &out); err != nil {
		return Txn{}, err
	}
	return out, nil
}

func (t *TransactionsAPI) List(ctx context.Context, req ListTransactionsRequest) (ListTransactionsResponse, error) {
	query := url.Values{}
	if req.StartTime != nil {
		query.Set("start_time", req.StartTime.UTC().Format(time.RFC3339))
	}
	if req.EndTime != nil {
		query.Set("end_time", req.EndTime.UTC().Format(time.RFC3339))
	}
	if v := strings.TrimSpace(req.Status); v != "" {
		query.Set("status", strings.ToUpper(v))
	}
	if v := strings.TrimSpace(req.TransferScene); v != "" {
		query.Set("transfer_scene", strings.ToUpper(v))
	}
	if v := strings.TrimSpace(req.OutUserID); v != "" {
		query.Set("out_user_id", v)
	}
	if req.PageSize > 0 {
		query.Set("page_size", fmt.Sprintf("%d", req.PageSize))
	}
	if v := strings.TrimSpace(req.PageToken); v != "" {
		query.Set("page_token", v)
	}
	var out ListTransactionsResponse
	if err := t.client.do(ctx, "GET", "/api/v1/transactions", query, nil, &out); err != nil {
		return ListTransactionsResponse{}, err
	}
	if out.Items == nil {
		out.Items = make([]Txn, 0)
	}
	return out, nil
}

func (t *TransactionsAPI) ListAccountChangeLogs(ctx context.Context, accountNo string, req ListAccountChangeLogsRequest) (ListAccountChangeLogsResponse, error) {
	accountNo = strings.TrimSpace(accountNo)
	if accountNo == "" {
		return ListAccountChangeLogsResponse{}, fmt.Errorf("account_no is required")
	}

	query := url.Values{}
	if req.PageSize > 0 {
		query.Set("page_size", fmt.Sprintf("%d", req.PageSize))
	}
	if v := strings.TrimSpace(req.PageToken); v != "" {
		query.Set("page_token", v)
	}

	var out ListAccountChangeLogsResponse
	if err := t.client.do(ctx, "GET", "/api/v1/accounts/"+url.PathEscape(accountNo)+"/change-logs", query, nil, &out); err != nil {
		return ListAccountChangeLogsResponse{}, err
	}
	if out.Items == nil {
		out.Items = make([]AccountChangeLog, 0)
	}
	return out, nil
}

func normalizeOutTradeNoAndAmount(outTradeNo string, amount int64) (string, error) {
	trimmed := strings.TrimSpace(outTradeNo)
	if trimmed == "" {
		return "", fmt.Errorf("out_trade_no is required")
	}
	if !outTradeNoPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid out_trade_no")
	}
	if amount <= 0 {
		return "", fmt.Errorf("amount must be > 0")
	}
	return trimmed, nil
}
