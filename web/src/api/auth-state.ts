// The session's CSRF token and what to do when the API answers 401. Kept outside React
// so the API client's middleware can read them.
let csrf = "";
let unauthorized: (() => void) | null = null;

export function setCsrfToken(token: string | undefined) {
  csrf = token ?? "";
}

export function csrfToken() {
  return csrf;
}

export function onUnauthorized(handler: (() => void) | null) {
  unauthorized = handler;
}

export function notifyUnauthorized() {
  unauthorized?.();
}
