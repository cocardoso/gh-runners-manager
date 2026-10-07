const KEY = "ghrm.adminToken";

/** The admin token lives in sessionStorage until M5 adds sign-in. */
export function getAdminToken(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function setAdminToken(token: string | null) {
  try {
    if (token) sessionStorage.setItem(KEY, token);
    else sessionStorage.removeItem(KEY);
  } catch {
    // storage unavailable: the token is asked for again next time
  }
}
