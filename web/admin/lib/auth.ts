const ACCESS_TOKEN_KEY = "coin_admin_access_token";
const REFRESH_TOKEN_KEY = "coin_admin_refresh_token";
const USERNAME_KEY = "coin_admin_username";

export function getAccessToken(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(ACCESS_TOKEN_KEY) ?? "";
}

export function getRefreshToken(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(REFRESH_TOKEN_KEY) ?? "";
}

export function getAdminUsername(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(USERNAME_KEY) ?? "";
}

export function saveAuth(accessToken: string, refreshToken: string, username: string): void {
  if (typeof window === "undefined") {
    return;
  }
  window.localStorage.setItem(ACCESS_TOKEN_KEY, accessToken);
  window.localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken);
  window.localStorage.setItem(USERNAME_KEY, username);
}

export function clearAuth(): void {
  if (typeof window === "undefined") {
    return;
  }
  window.localStorage.removeItem(ACCESS_TOKEN_KEY);
  window.localStorage.removeItem(REFRESH_TOKEN_KEY);
  window.localStorage.removeItem(USERNAME_KEY);
}
