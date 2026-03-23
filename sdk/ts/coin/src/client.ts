import { createHmac, createHash, randomBytes } from "node:crypto";
import { CoinAPIError } from "./error.js";
import { MerchantAPI } from "./merchant.js";
import { TransactionsAPI } from "./transactions.js";
import type { ClientOptions } from "./types.js";

const SUCCESS_CODE = "SUCCESS";
const MAX_RESPONSE_BODY_BYTES = 4 * 1024 * 1024;

interface Envelope {
  code: string;
  message: string;
  request_id: string;
  data?: unknown;
}

export class CoinClient {
  /** @internal */ readonly _baseURL: URL;
  /** @internal */ readonly _merchantNo: string;
  /** @internal */ readonly _merchantSecret: string;
  /** @internal */ readonly _fetch: typeof globalThis.fetch;
  /** @internal */ readonly _timeout: number;
  /** @internal */ readonly _now: () => Date;
  /** @internal */ readonly _nonce: () => string;
  /** @internal */ readonly _userAgent: string;

  readonly merchant: MerchantAPI;
  readonly transactions: TransactionsAPI;

  constructor(opts: ClientOptions) {
    const base = (opts.baseURL ?? "").trim();
    if (!base) throw new Error("base_url is required");

    let parsed: URL;
    try {
      parsed = new URL(base);
    } catch {
      throw new Error(`parse base_url: invalid URL: ${base}`);
    }
    if (!parsed.protocol || !parsed.host) {
      throw new Error("base_url must include scheme and host");
    }

    const merchantNo = (opts.merchantNo ?? "").trim();
    if (!merchantNo) throw new Error("merchant_no is required");
    if (!(opts.merchantSecret ?? "").trim()) throw new Error("merchant_secret is required");

    this._baseURL = parsed;
    this._merchantNo = merchantNo;
    this._merchantSecret = opts.merchantSecret;
    this._fetch = opts.fetch ?? globalThis.fetch;
    this._timeout = opts.timeout && opts.timeout > 0 ? opts.timeout : 10_000;
    this._now = opts.now ?? (() => new Date());
    this._nonce = opts.nonceGenerator ?? (() => randomBytes(16).toString("hex"));
    this._userAgent = (opts.userAgent ?? "").trim();

    this.merchant = new MerchantAPI(this);
    this.transactions = new TransactionsAPI(this);
  }

  /** @internal */
  async _do<T>(
    method: string,
    path: string,
    query?: Record<string, string>,
    payload?: unknown,
  ): Promise<T> {
    if (!path || path[0] !== "/") {
      throw new Error("path must start with /");
    }

    const body = payload != null ? JSON.stringify(payload) : "";
    const bodyBytes = new TextEncoder().encode(body);

    const fullPath = joinURLPath(this._baseURL.pathname, path);
    const url = new URL(fullPath, this._baseURL);
    if (query) {
      for (const [k, v] of Object.entries(query)) {
        url.searchParams.set(k, v);
      }
    }

    const timestamp = String(this._now().getTime());
    const nonce = this._nonce().trim();
    if (!nonce) throw new Error("nonce generator returned empty value");

    const sig = signature(method, fullPath, this._merchantNo, timestamp, nonce, bodyBytes, this._merchantSecret);

    const headers: Record<string, string> = {
      "X-Merchant-No": this._merchantNo,
      "X-Timestamp": timestamp,
      "X-Nonce": nonce,
      "X-Signature": sig,
    };
    if (payload != null) {
      headers["Content-Type"] = "application/json";
    }
    if (this._userAgent) {
      headers["User-Agent"] = this._userAgent;
    }

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this._timeout);

    let resp: Response;
    try {
      resp = await this._fetch(url.toString(), {
        method: method.toUpperCase(),
        headers,
        body: payload != null ? body : undefined,
        signal: controller.signal,
      });
    } finally {
      clearTimeout(timer);
    }

    const respBody = await resp.text();
    if (new TextEncoder().encode(respBody).byteLength > MAX_RESPONSE_BODY_BYTES) {
      throw new CoinAPIError({
        httpStatus: resp.status,
        code: "RESPONSE_TOO_LARGE",
        message: `response body exceeds ${MAX_RESPONSE_BODY_BYTES} bytes`,
        rawBody: respBody.slice(0, MAX_RESPONSE_BODY_BYTES),
      });
    }

    let env: Envelope;
    try {
      env = JSON.parse(respBody);
    } catch {
      throw new CoinAPIError({
        httpStatus: resp.status,
        code: "INVALID_RESPONSE",
        message: respBody,
        rawBody: respBody,
      });
    }

    if (!env.code) env.code = "INVALID_RESPONSE";
    if (env.code !== SUCCESS_CODE || resp.status >= 400) {
      throw new CoinAPIError({
        httpStatus: resp.status,
        code: env.code,
        message: env.message,
        requestId: env.request_id,
        rawBody: respBody,
      });
    }

    return (env.data ?? null) as T;
  }
}

export function signature(
  method: string,
  path: string,
  merchantNo: string,
  timestamp: string,
  nonce: string,
  body: Uint8Array,
  secret: string,
): string {
  const bodyHash = createHash("sha256").update(body).digest("hex");
  const signingString = [
    method.toUpperCase().trim(),
    path,
    merchantNo,
    timestamp,
    nonce,
    bodyHash,
  ].join("\n");
  return createHmac("sha256", secret).update(signingString).digest("hex");
}

function joinURLPath(basePath: string, p: string): string {
  let left = (basePath ?? "").trim().replace(/\/+$/, "");
  const right = (p ?? "").trim().replace(/^\/+/, "");
  if (!left) return "/" + right;
  if (!left.startsWith("/")) left = "/" + left;
  return left + "/" + right;
}
