import type { CoinClient } from "./client.js";
import type { MerchantProfile } from "./types.js";

export class MerchantAPI {
  /** @internal */
  private readonly client: CoinClient;

  /** @internal */
  constructor(client: CoinClient) {
    this.client = client;
  }

  async me(): Promise<MerchantProfile> {
    return this.client._do<MerchantProfile>("GET", "/api/v1/merchants/me");
  }
}
