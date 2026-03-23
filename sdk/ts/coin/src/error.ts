export class CoinAPIError extends Error {
  readonly httpStatus: number;
  readonly code: string;
  readonly requestId: string;
  readonly rawBody: string;

  constructor(opts: {
    httpStatus: number;
    code: string;
    message: string;
    requestId?: string;
    rawBody?: string;
  }) {
    const parts = [`api error: http=${opts.httpStatus} code=${opts.code} message=${opts.message}`];
    if (opts.requestId) {
      parts.push(`request_id=${opts.requestId}`);
    }
    super(parts.join(" "));
    this.name = "CoinAPIError";
    this.httpStatus = opts.httpStatus;
    this.code = opts.code;
    this.requestId = opts.requestId ?? "";
    this.rawBody = opts.rawBody ?? "";
  }
}
