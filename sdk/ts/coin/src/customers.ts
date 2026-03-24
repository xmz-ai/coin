import type { CoinClient } from "./client.js";
import type { CustomerBalance, ListBooksResponse, ListBookCreditChangeLogsResponse } from "./types.js";

export class CustomersAPI {
  /** @internal */
  private readonly client: CoinClient;

  /** @internal */
  constructor(client: CoinClient) {
    this.client = client;
  }

  async getBalance(outUserID: string): Promise<CustomerBalance> {
    outUserID = (outUserID ?? "").trim();
    if (!outUserID) throw new Error("out_user_id is required");
    return this.client._do<CustomerBalance>(
      "GET",
      "/api/v1/customers/balance",
      { out_user_id: outUserID },
    );
  }

  async listBooks(outUserID: string): Promise<ListBooksResponse> {
    outUserID = (outUserID ?? "").trim();
    if (!outUserID) throw new Error("out_user_id is required");
    return this.client._do<ListBooksResponse>(
      "GET",
      "/api/v1/customers/books",
      { out_user_id: outUserID },
    );
  }

  async listBookCreditChangeLogs(bookNo: string): Promise<ListBookCreditChangeLogsResponse> {
    bookNo = (bookNo ?? "").trim();
    if (!bookNo) throw new Error("book_no is required");
    return this.client._do<ListBookCreditChangeLogsResponse>(
      "GET",
      `/api/v1/customers/books/${bookNo}/change-logs`,
    );
  }
}
