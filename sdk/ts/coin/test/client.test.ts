import { describe, it, expect } from "vitest";
import http from "node:http";
import { CoinClient, CoinAPIError, signature } from "../src/index.js";
import type { CreditRequest } from "../src/index.js";

function startServer(handler: http.RequestListener): Promise<{ url: string; close: () => void }> {
  return new Promise((resolve) => {
    const server = http.createServer(handler);
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address() as { port: number };
      resolve({
        url: `http://127.0.0.1:${addr.port}`,
        close: () => server.close(),
      });
    });
  });
}

function collectBody(req: http.IncomingMessage): Promise<string> {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (chunk: Buffer) => (data += chunk.toString()));
    req.on("end", () => resolve(data));
  });
}

describe("CoinClient", () => {
  const merchantNo = "1000123456789012";
  const secret = "msk_test_secret";
  const nonce = "nonce-fixed";
  const fixedNow = new Date("2026-03-22T10:00:00.000Z");
  const expectedTS = String(fixedNow.getTime());

  it("signs headers and body on credit", async () => {
    const server = await startServer(async (req, res) => {
      expect(req.method).toBe("POST");
      expect(req.url).toBe("/api/v1/transactions/credit");
      expect(req.headers["x-merchant-no"]).toBe(merchantNo);
      expect(req.headers["x-nonce"]).toBe(nonce);
      expect(req.headers["x-timestamp"]).toBe(expectedTS);

      const body = await collectBody(req);
      const parsed: CreditRequest = JSON.parse(body);
      expect(parsed.out_trade_no).toBe("ord_001");
      expect(parsed.user_id).toBe("u_1");
      expect(parsed.amount).toBe(100);

      const expectedSig = signature(
        "POST",
        "/api/v1/transactions/credit",
        merchantNo,
        expectedTS,
        nonce,
        new TextEncoder().encode(body),
        secret,
      );
      expect(req.headers["x-signature"]).toBe(expectedSig);

      res.writeHead(201, { "Content-Type": "application/json" });
      res.end(JSON.stringify({
        code: "SUCCESS",
        message: "ok",
        request_id: "req_1",
        data: { txn_no: "txn_1", status: "INIT" },
      }));
    });

    try {
      const client = new CoinClient({
        baseURL: server.url,
        merchantNo,
        merchantSecret: secret,
        now: () => fixedNow,
        nonceGenerator: () => nonce,
      });

      const resp = await client.transactions.credit({
        out_trade_no: "ord_001",
        user_id: "u_1",
        amount: 100,
      });

      expect(resp.txn_no).toBe("txn_1");
      expect(resp.status).toBe("INIT");
    } finally {
      server.close();
    }
  });

  it("returns CoinAPIError on business failure", async () => {
    const server = await startServer(async (_req, res) => {
      res.writeHead(409, { "Content-Type": "application/json" });
      res.end(JSON.stringify({
        code: "DUPLICATE_OUT_TRADE_NO",
        message: "duplicate",
        request_id: "req_dup",
      }));
    });

    try {
      const client = new CoinClient({
        baseURL: server.url,
        merchantNo: "1000123456789012",
        merchantSecret: "s",
        nonceGenerator: () => "n",
        now: () => new Date(0),
      });

      await expect(
        client.transactions.credit({ out_trade_no: "ord_dup", user_id: "u_1", amount: 1 }),
      ).rejects.toThrow(CoinAPIError);

      try {
        await client.transactions.credit({ out_trade_no: "ord_dup", user_id: "u_1", amount: 1 });
      } catch (err) {
        expect(err).toBeInstanceOf(CoinAPIError);
        const apiErr = err as CoinAPIError;
        expect(apiErr.code).toBe("DUPLICATE_OUT_TRADE_NO");
        expect(apiErr.httpStatus).toBe(409);
        expect(apiErr.requestId).toBe("req_dup");
      }
    } finally {
      server.close();
    }
  });

  it("builds query params for list transactions", async () => {
    const start = new Date("2026-03-21T12:00:00.000Z");
    const end = new Date("2026-03-22T12:00:00.000Z");

    const server = await startServer(async (req, res) => {
      const url = new URL(req.url!, `http://${req.headers.host}`);
      expect(url.pathname).toBe("/api/v1/transactions");
      expect(url.searchParams.get("start_time")).toBe("2026-03-21T12:00:00.000Z");
      expect(url.searchParams.get("end_time")).toBe("2026-03-22T12:00:00.000Z");
      expect(url.searchParams.get("status")).toBe("RECV_SUCCESS");
      expect(url.searchParams.get("transfer_scene")).toBe("ISSUE");
      expect(url.searchParams.get("out_user_id")).toBe("u_100");
      expect(url.searchParams.get("page_size")).toBe("50");
      expect(url.searchParams.get("page_token")).toBe("tok_1");

      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({
        code: "SUCCESS",
        message: "ok",
        request_id: "req_list",
        data: {
          items: [{
            txn_no: "t1", out_trade_no: "o1", transfer_scene: "ISSUE",
            status: "RECV_SUCCESS", amount: 10, refundable_amount: 10,
            debit_account_no: "a1", credit_account_no: "a2",
            error_code: "", error_msg: "", created_at: "2026-03-22T12:00:00Z",
          }],
          next_page_token: "tok_2",
        },
      }));
    });

    try {
      const client = new CoinClient({
        baseURL: server.url,
        merchantNo: "1000123456789012",
        merchantSecret: "s",
        nonceGenerator: () => "n",
        now: () => new Date(0),
      });

      const resp = await client.transactions.list({
        startTime: start,
        endTime: end,
        status: "recv_success",
        transferScene: "issue",
        outUserID: "u_100",
        pageSize: 50,
        pageToken: "tok_1",
      });

      expect(resp.next_page_token).toBe("tok_2");
      expect(resp.items).toHaveLength(1);
      expect(resp.items[0].txn_no).toBe("t1");
    } finally {
      server.close();
    }
  });

  it("throws validation error for empty out_trade_no", async () => {
    const client = new CoinClient({
      baseURL: "https://example.com",
      merchantNo: "1000123456789012",
      merchantSecret: "s",
      nonceGenerator: () => "n",
    });

    await expect(
      client.transactions.credit({ out_trade_no: "", amount: 1, user_id: "u" }),
    ).rejects.toThrow(/out_trade_no/);
  });

  it("trims out_trade_no before sending", async () => {
    const server = await startServer(async (req, res) => {
      const body = await collectBody(req);
      const parsed: CreditRequest = JSON.parse(body);
      expect(parsed.out_trade_no).toBe("ord_001");

      const expectedSig = signature(
        "POST",
        "/api/v1/transactions/credit",
        merchantNo,
        expectedTS,
        nonce,
        new TextEncoder().encode(body),
        secret,
      );
      expect(req.headers["x-signature"]).toBe(expectedSig);

      res.writeHead(201, { "Content-Type": "application/json" });
      res.end(JSON.stringify({
        code: "SUCCESS",
        message: "ok",
        request_id: "req_1",
        data: { txn_no: "txn_1", status: "INIT" },
      }));
    });

    try {
      const client = new CoinClient({
        baseURL: server.url,
        merchantNo,
        merchantSecret: secret,
        now: () => fixedNow,
        nonceGenerator: () => nonce,
      });

      const resp = await client.transactions.credit({
        out_trade_no: "  ord_001  ",
        user_id: "u_1",
        amount: 100,
      });
      expect(resp.txn_no).toBe("txn_1");
    } finally {
      server.close();
    }
  });

  it("throws RESPONSE_TOO_LARGE for oversized body", async () => {
    const huge = "x".repeat(4 * 1024 * 1024 + 1);
    const server = await startServer(async (_req, res) => {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(`{"code":"SUCCESS","message":"ok","request_id":"req_1","data":"${huge}"}`);
    });

    try {
      const client = new CoinClient({
        baseURL: server.url,
        merchantNo: "1000123456789012",
        merchantSecret: "s",
        nonceGenerator: () => "n",
        now: () => new Date(0),
      });

      try {
        await client.merchant.me();
        expect.fail("should have thrown");
      } catch (err) {
        expect(err).toBeInstanceOf(CoinAPIError);
        expect((err as CoinAPIError).code).toBe("RESPONSE_TOO_LARGE");
      }
    } finally {
      server.close();
    }
  });
});
