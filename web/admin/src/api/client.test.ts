import { describe, expect, it, vi } from 'vitest';
import { apiFetch, setCSRFToken } from './client';

describe('apiFetch', () => {
  it('adds csrf header to mutating admin requests', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setCSRFToken('csrf-test');

    await apiFetch('/admin/api/gateway-keys', { method: 'POST', body: JSON.stringify({ name: 'x' }) });

    expect(fetchMock.mock.calls[0][1].headers['X-CSRF-Token']).toBe('csrf-test');
  });
});
