import type { CoinClient } from "./client.js";
import type { CustomerBalance } from "./types.js";

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
}
