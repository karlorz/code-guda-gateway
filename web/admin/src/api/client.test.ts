import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiFetch, clearCSRFToken, setAuthFailureHandler, setCSRFToken } from './client';

describe('apiFetch', () => {
  afterEach(() => {
    clearCSRFToken();
    setAuthFailureHandler(undefined);
    vi.unstubAllGlobals();
  });

  it('adds csrf header to mutating admin requests', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setCSRFToken('csrf-test');

    await apiFetch('/admin/api/gateway-keys', { method: 'POST', body: JSON.stringify({ name: 'x' }) });

    expect(fetchMock.mock.calls[0][1].headers['X-CSRF-Token']).toBe('csrf-test');
  });

  it('surfaces structured API error messages', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: { code: 'invalid_credentials', message: 'Invalid admin token. Check the password and try again.' } }), {
          status: 401,
        }),
      ),
    );

    await expect(apiFetch('/admin/api/login', { method: 'POST', body: '{}' })).rejects.toThrow(
      'Invalid admin token. Check the password and try again.',
    );
  });

  it('uses a login-specific fallback when the body is not structured JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('unauthorized', { status: 401 })));

    await expect(apiFetch('/admin/api/login', { method: 'POST', body: '{}' })).rejects.toThrow(
      'Invalid admin token. Check the password and try again.',
    );
  });

  it('does not treat a failed login as a session drop', async () => {
    const onAuthFailure = vi.fn();
    setAuthFailureHandler(onAuthFailure);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('unauthorized', { status: 401 })));

    await expect(apiFetch('/admin/api/login', { method: 'POST', body: '{}' })).rejects.toThrow();
    expect(onAuthFailure).not.toHaveBeenCalled();
  });
});
