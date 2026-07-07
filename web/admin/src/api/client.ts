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
  if (res.status === 401 || res.status === 403) {
    clearCSRFToken();
    authFailureHandler?.();
  }
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw new Error(body?.error?.message || `request failed: ${res.status}`);
  }
  return res.json() as Promise<T>;
}
