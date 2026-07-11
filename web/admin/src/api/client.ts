let csrfToken = '';
let authFailureHandler: (() => void) | undefined;

export function setCSRFToken(token: string) {
  csrfToken = token;
}

export function clearCSRFToken() {
  csrfToken = '';
}

export function setAuthFailureHandler(handler: (() => void) | undefined) {
  authFailureHandler = handler;
}

function errorMessageFromBody(body: unknown, status: number, path: string): string {
  if (body && typeof body === 'object') {
    const err = (body as { error?: { message?: unknown } }).error;
    if (err && typeof err.message === 'string' && err.message.trim()) {
      return err.message;
    }
  }

  const isLoginAttempt = path.includes('/admin/api/login');
  if (isLoginAttempt && (status === 401 || status === 403)) {
    return 'Invalid admin token. Check the password and try again.';
  }
  if (status === 401 || status === 403) {
    return 'Session expired or not authorized. Please log in again.';
  }
  if (typeof body === 'string' && body.trim()) {
    return body.trim();
  }
  return `request failed: ${status}`;
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method || 'GET').toUpperCase();
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...(init.body ? { 'Content-Type': 'application/json' } : {}),
    ...(init.headers as Record<string, string> | undefined),
  };
  if (['POST', 'PATCH', 'DELETE'].includes(method) && csrfToken) {
    headers['X-CSRF-Token'] = csrfToken;
  }
  const res = await fetch(path, { ...init, method, headers, credentials: 'include' });
  // Failed login is a credentials error, not a dropped session — leave the login form alone.
  const isLoginAttempt = path.includes('/admin/api/login');
  if ((res.status === 401 || res.status === 403) && !isLoginAttempt) {
    clearCSRFToken();
    authFailureHandler?.();
  }
  if (!res.ok) {
    const text = await res.text();
    let body: unknown = null;
    if (text) {
      try {
        body = JSON.parse(text);
      } catch {
        body = text;
      }
    }
    throw new Error(errorMessageFromBody(body, res.status, path));
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}
