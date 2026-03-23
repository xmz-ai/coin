import { clearAuth, getAccessToken } from "./auth";

export type APIEnvelope<T> = {
  code: string;
  message: string;
  request_id: string;
  data?: T;
};

export class APIError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

const API_BASE = process.env.NEXT_PUBLIC_ADMIN_API_BASE ?? "/admin/api/v1";

function redirectToLoginAfterAuthFailure(): void {
  if (typeof window === "undefined") {
    return;
  }
  clearAuth();
  if (window.location.pathname !== "/login") {
    window.location.replace("/login");
  }
}

export async function apiRequest<T>(
  path: string,
  init: RequestInit = {},
  options: { auth?: boolean } = { auth: true }
): Promise<T> {
  const headers = new Headers(init.headers ?? {});
  if (!headers.has("Content-Type") && init.body) {
    headers.set("Content-Type", "application/json");
  }

  if (options.auth !== false) {
    const token = getAccessToken();
    if (token) {
      headers.set("Authorization", `Bearer ${token}`);
    }
  }

  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers,
    cache: "no-store",
  });

  const text = await res.text();
  let envelope: APIEnvelope<T> | null = null;
  if (text) {
    try {
      envelope = JSON.parse(text) as APIEnvelope<T>;
    } catch {
      throw new APIError("INVALID_RESPONSE", "invalid response body", res.status);
    }
  }

  const isAuthRequest = options.auth !== false;
  const errorCode = envelope?.code ?? "HTTP_ERROR";
  if (!res.ok || !envelope || envelope.code !== "SUCCESS") {
    if (
      isAuthRequest &&
      (res.status === 401 || errorCode === "ADMIN_AUTH_REQUIRED" || errorCode === "ADMIN_AUTH_INVALID")
    ) {
      redirectToLoginAfterAuthFailure();
    }
    throw new APIError(
      errorCode,
      envelope?.message ?? `request failed with ${res.status}`,
      res.status
    );
  }

  return envelope.data as T;
}
