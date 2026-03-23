export type SetupInitializeResponse = {
  admin_username: string;
  merchant_no: string;
  merchant_secret: string;
  secret_version: number;
  budget_account_no: string;
  receivable_account_no: string;
};

const SETUP_RESULT_KEY = "coin_admin_setup_result";

export function saveSetupResult(result: SetupInitializeResponse): void {
  if (typeof window === "undefined") {
    return;
  }
  window.sessionStorage.setItem(SETUP_RESULT_KEY, JSON.stringify(result));
}

export function getSetupResult(): SetupInitializeResponse | null {
  if (typeof window === "undefined") {
    return null;
  }
  const raw = window.sessionStorage.getItem(SETUP_RESULT_KEY);
  if (!raw) {
    return null;
  }
  try {
    return JSON.parse(raw) as SetupInitializeResponse;
  } catch {
    return null;
  }
}

export function clearSetupResult(): void {
  if (typeof window === "undefined") {
    return;
  }
  window.sessionStorage.removeItem(SETUP_RESULT_KEY);
}
