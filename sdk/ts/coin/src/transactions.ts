import type { CoinClient } from "./client.js";
import type {
  CreditRequest,
  DebitRequest,
  TransferRequest,
  RefundRequest,
  ListTransactionsRequest,
  TxnSubmitResponse,
  Txn,
  ListTransactionsResponse,
  ListAccountChangeLogsRequest,
  ListAccountChangeLogsResponse,
} from "./types.js";

const OUT_TRADE_NO_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

export class TransactionsAPI {
  /** @internal */
  private readonly client: CoinClient;

  /** @internal */
  constructor(client: CoinClient) {
    this.client = client;
  }

  async credit(req: CreditRequest): Promise<TxnSubmitResponse> {
    req = { ...req, out_trade_no: validateOutTradeNoAndAmount(req.out_trade_no, req.amount) };
    if (!(req.credit_account_no ?? "").trim() && !(req.user_id ?? "").trim()) {
      throw new Error("credit_account_no or user_id is required");
    }
    if ((req.expire_in_days ?? 0) < 0) {
      throw new Error("expire_in_days must be >= 0");
    }
    return this.client._do<TxnSubmitResponse>("POST", "/api/v1/transactions/credit", undefined, req);
  }

  async debit(req: DebitRequest): Promise<TxnSubmitResponse> {
    req = { ...req, out_trade_no: validateOutTradeNoAndAmount(req.out_trade_no, req.amount) };
    if (!(req.debit_account_no ?? "").trim() && !(req.debit_out_user_id ?? "").trim()) {
      throw new Error("debit_account_no or debit_out_user_id is required");
    }
    return this.client._do<TxnSubmitResponse>("POST", "/api/v1/transactions/debit", undefined, req);
  }

  async transfer(req: TransferRequest): Promise<TxnSubmitResponse> {
    req = { ...req, out_trade_no: validateOutTradeNoAndAmount(req.out_trade_no, req.amount) };
    if (!(req.from_account_no ?? "").trim() && !(req.from_out_user_id ?? "").trim()) {
      throw new Error("from_account_no or from_out_user_id is required");
    }
    if (!(req.to_account_no ?? "").trim() && !(req.to_out_user_id ?? "").trim()) {
      throw new Error("to_account_no or to_out_user_id is required");
    }
    if ((req.to_expire_in_days ?? 0) < 0) {
      throw new Error("to_expire_in_days must be >= 0");
    }
    return this.client._do<TxnSubmitResponse>("POST", "/api/v1/transactions/transfer", undefined, req);
  }

  async refund(req: RefundRequest): Promise<TxnSubmitResponse> {
    req = { ...req, out_trade_no: validateOutTradeNoAndAmount(req.out_trade_no, req.amount) };
    const refundOfTxnNo = (req.refund_of_txn_no ?? "").trim();
    if (!refundOfTxnNo) throw new Error("refund_of_txn_no is required");
    req = { ...req, refund_of_txn_no: refundOfTxnNo };
    return this.client._do<TxnSubmitResponse>("POST", "/api/v1/transactions/refund", undefined, req);
  }

  async getByTxnNo(txnNo: string): Promise<Txn> {
    txnNo = (txnNo ?? "").trim();
    if (!txnNo) throw new Error("txn_no is required");
    return this.client._do<Txn>("GET", `/api/v1/transactions/${encodeURIComponent(txnNo)}`);
  }

  async getByOutTradeNo(outTradeNo: string): Promise<Txn> {
    outTradeNo = (outTradeNo ?? "").trim();
    if (!outTradeNo) throw new Error("out_trade_no is required");
    return this.client._do<Txn>("GET", `/api/v1/transactions/by-out-trade-no/${encodeURIComponent(outTradeNo)}`);
  }

  async list(req: ListTransactionsRequest): Promise<ListTransactionsResponse> {
    const query: Record<string, string> = {};
    if (req.startTime) query.start_time = req.startTime.toISOString();
    if (req.endTime) query.end_time = req.endTime.toISOString();
    if ((req.status ?? "").trim()) query.status = req.status!.trim().toUpperCase();
    if ((req.transferScene ?? "").trim()) query.transfer_scene = req.transferScene!.trim().toUpperCase();
    if ((req.outUserID ?? "").trim()) query.out_user_id = req.outUserID!.trim();
    if (req.pageSize && req.pageSize > 0) query.page_size = String(req.pageSize);
    if ((req.pageToken ?? "").trim()) query.page_token = req.pageToken!.trim();

    const resp = await this.client._do<ListTransactionsResponse>(
      "GET",
      "/api/v1/transactions",
      Object.keys(query).length > 0 ? query : undefined,
    );
    return { ...resp, items: resp.items ?? [] };
  }

  async listAccountChangeLogs(accountNo: string, req: ListAccountChangeLogsRequest = {}): Promise<ListAccountChangeLogsResponse> {
    accountNo = (accountNo ?? "").trim();
    if (!accountNo) throw new Error("account_no is required");

    const query: Record<string, string> = {};
    if (req.pageSize && req.pageSize > 0) query.page_size = String(req.pageSize);
    if ((req.pageToken ?? "").trim()) query.page_token = req.pageToken!.trim();

    const resp = await this.client._do<ListAccountChangeLogsResponse>(
      "GET",
      `/api/v1/accounts/${encodeURIComponent(accountNo)}/change-logs`,
      Object.keys(query).length > 0 ? query : undefined,
    );
    return { ...resp, items: resp.items ?? [] };
  }
}

function validateOutTradeNoAndAmount(outTradeNo: string, amount: number): string {
  const trimmed = (outTradeNo ?? "").trim();
  if (!trimmed) throw new Error("out_trade_no is required");
  if (!OUT_TRADE_NO_PATTERN.test(trimmed)) throw new Error("invalid out_trade_no");
  if (!amount || amount <= 0) throw new Error("amount must be > 0");
  return trimmed;
}
