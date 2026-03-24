export interface ClientOptions {
  baseURL: string;
  merchantNo: string;
  merchantSecret: string;
  timeout?: number;
  fetch?: typeof globalThis.fetch;
  userAgent?: string;
  /** Injected for testing. Returns current UTC time. */
  now?: () => Date;
  /** Injected for testing. Returns a nonce string. */
  nonceGenerator?: () => string;
}

export interface CreditRequest {
  out_trade_no: string;
  title?: string;
  remark?: string;
  debit_account_no?: string;
  credit_account_no?: string;
  user_id?: string;
  expire_in_days?: number;
  amount: number;
}

export interface DebitRequest {
  out_trade_no: string;
  title?: string;
  remark?: string;
  biz_type?: string;
  transfer_scene?: string;
  debit_account_no?: string;
  debit_out_user_id?: string;
  credit_account_no?: string;
  credit_out_user_id?: string;
  amount: number;
}

export interface TransferRequest {
  out_trade_no: string;
  title?: string;
  remark?: string;
  biz_type?: string;
  transfer_scene?: string;
  from_account_no?: string;
  from_out_user_id?: string;
  to_account_no?: string;
  to_out_user_id?: string;
  to_expire_in_days?: number;
  amount: number;
}

export interface RefundRequest {
  out_trade_no: string;
  title?: string;
  remark?: string;
  biz_type?: string;
  refund_of_txn_no: string;
  amount: number;
}

export interface ListTransactionsRequest {
  startTime?: Date;
  endTime?: Date;
  status?: string;
  transferScene?: string;
  outUserID?: string;
  pageSize?: number;
  pageToken?: string;
}

export interface TxnSubmitResponse {
  txn_no: string;
  status: string;
}

export interface Txn {
  txn_no: string;
  out_trade_no: string;
  title: string;
  remark: string;
  transfer_scene: string;
  status: string;
  amount: number;
  refundable_amount: number;
  debit_account_no: string;
  credit_account_no: string;
  error_code: string;
  error_msg: string;
  created_at: string;
}

export interface ListTransactionsResponse {
  items: Txn[];
  next_page_token: string;
}

export interface AccountChangeLog {
  change_id: number;
  txn_no: string;
  account_no: string;
  delta: number;
  balance_before: number;
  balance_after: number;
  title: string;
  remark: string;
  created_at: string;
}

export interface ListAccountChangeLogsRequest {
  pageSize?: number;
  pageToken?: string;
}

export interface ListAccountChangeLogsResponse {
  items: AccountChangeLog[];
  next_page_token: string;
}

export interface CustomerBalance {
  out_user_id: string;
  account_no: string;
  balance: number;
  book_enabled: boolean;
}

export interface MerchantProfile {
  merchant_no: string;
  name: string;
  status: string;
  budget_account_no: string;
  receivable_account_no: string;
  secret_version: number;
  auto_create_account_on_customer_create: boolean;
  auto_create_customer_on_credit: boolean;
}
