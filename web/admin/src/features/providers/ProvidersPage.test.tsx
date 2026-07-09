import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { ProvidersPage } from './ProvidersPage';
import * as client from '../../api/client';

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return { ...actual, apiFetch: vi.fn() };
});

function renderWithClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

function emptyPool(provider: string) {
  return {
    provider,
    summary: {
      provider,
      key_count: 0,
      enabled_key_count: 0,
      available_key_count: 0,
      cooling_key_count: 0,
      refreshed_key_count: 0,
    },
    items: [],
    page: { limit: 25, offset: 0, total: 0 },
  };
}

describe('ProvidersPage pools', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings') {
        return { items: [{ provider: 'tavily', base_url: 'https://api.tavily.com' }] };
      }
      if (path === '/admin/api/provider-health') {
        return { items: [{ provider: 'tavily', status: 'healthy', key_count: 1, enabled_key_count: 1, cooldown_key_count: 0, reasons: [] }] };
      }
      if (path === '/admin/api/provider-quotas') {
        return { items: [] };
      }
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: {
            provider: 'tavily',
            key_count: 3,
            enabled_key_count: 3,
            available_key_count: 2,
            cooling_key_count: 1,
            refreshed_key_count: 2,
            known_remaining: 427,
          },
          items: [
            {
              status: 'cooling',
              key: { id: 5, provider: 'tavily', name: 'tavily-1', fingerprint: '1c105c', enabled: true, cooldown_reason: 'plan_limit_exceeded' },
              quota: { provider_key_id: 5, provider: 'tavily', available: false, source: 'tavily_usage', checked_at: '2026-07-09T00:00:00Z' },
            },
            {
              status: 'available',
              key: { id: 6, provider: 'tavily', name: 'tavily-2', fingerprint: 'bce42f', enabled: true },
              quota: { provider_key_id: 6, provider: 'tavily', available: true, source: 'tavily_usage', remaining: 427, limit_value: 1000, checked_at: '2026-07-09T00:01:00Z' },
            },
          ],
          page: { limit: 25, offset: 0, total: 3 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        const provider = path.split('/')[4].split('?')[0];
        return emptyPool(provider);
      }
      if (path === '/admin/api/provider-key-quotas/tavily/refresh-all') {
        return {};
      }
      if (path === '/admin/api/provider-quotas/tavily/refresh') {
        return { provider: 'tavily', available: true, source: 'tavily_usage' };
      }
      throw new Error(`unexpected path ${path}`);
    });
  });

  it('shows provider pool summary and paginated key rows', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings') return { items: [] };
      if (path === '/admin/api/provider-health') return { items: [] };
      if (path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: {
            provider: 'tavily',
            key_count: 3,
            enabled_key_count: 3,
            available_key_count: 2,
            cooling_key_count: 1,
            refreshed_key_count: 2,
            known_remaining: 427,
          },
          items: [
            {
              status: 'cooling',
              key: { id: 5, provider: 'tavily', name: 'tavily-1', fingerprint: '1c105c', enabled: true, cooldown_reason: 'plan_limit_exceeded' },
              quota: { provider_key_id: 5, provider: 'tavily', available: false, source: 'tavily_usage', checked_at: '2026-07-09T00:00:00Z' },
            },
            {
              status: 'available',
              key: { id: 6, provider: 'tavily', name: 'tavily-2', fingerprint: 'bce42f', enabled: true },
              quota: { provider_key_id: 6, provider: 'tavily', available: true, source: 'tavily_usage', remaining: 427, limit_value: 1000, checked_at: '2026-07-09T00:01:00Z' },
            },
          ],
          page: { limit: 25, offset: 0, total: 3 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return { provider: path.split('/')[4].split('?')[0], summary: {}, items: [], page: { limit: 25, offset: 0, total: 0 } };
      }
      throw new Error(`unexpected path ${path}`);
    });
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('Tavily Pool')).toBeInTheDocument();
    expect(screen.getByText(/Enabled 3/)).toBeInTheDocument();
    expect(screen.getByText('tavily-1')).toBeInTheDocument();
    expect(screen.getByText('plan_limit_exceeded')).toBeInTheDocument();
    expect(screen.getByText('427 / 1000 remaining')).toBeInTheDocument();
  });

  it('shows usage not "not refreshed" when quota row exists but remaining is null', async () => {
    // Tavily's real API returns used + account_plan_* in details but no
    // top-level key.limit, so the normalizer leaves remaining/limit_value
    // null. A refreshed, available key must not show "not refreshed".
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings' || path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: { provider: 'tavily', key_count: 1, enabled_key_count: 1, available_key_count: 1, cooling_key_count: 0, refreshed_key_count: 1 },
          items: [
            {
              status: 'available',
              key: { id: 6, provider: 'tavily', name: 'tavily-2', fingerprint: 'bce42f', enabled: true },
              quota: { provider_key_id: 6, provider: 'tavily', available: true, source: 'tavily_usage', used: 1, checked_at: '2026-07-09T00:01:00Z' },
            },
          ],
          page: { limit: 25, offset: 0, total: 1 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return { provider: path.split('/')[4].split('?')[0], summary: {}, items: [], page: { limit: 25, offset: 0, total: 0 } };
      }
      throw new Error(`unexpected path ${path}`);
    });
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    // refreshed key must NOT show "not refreshed"
    expect(screen.queryByText('not refreshed')).not.toBeInTheDocument();
    // it should show the usage it does have
    expect(screen.getByText(/used 1/i)).toBeInTheDocument();
  });

  it('reads cooldown reason from PascalCase Go JSON', async () => {
    // Go's DisplayProviderKey has no json tags, so the API emits PascalCase
    // (CooldownReason). valueOf must fall back to it when snake_case is absent.
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings' || path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: { provider: 'tavily', key_count: 1, enabled_key_count: 1, available_key_count: 0, cooling_key_count: 1, refreshed_key_count: 0 },
          items: [
            {
              status: 'cooling',
              key: { ID: 9, Provider: 'tavily', Name: 'tavily-pascal', Fingerprint: 'ff00ff', Enabled: true, CooldownReason: 'plan_limit_exceeded' },
              quota: null,
            },
          ],
          page: { limit: 25, offset: 0, total: 1 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return { provider: path.split('/')[4].split('?')[0], summary: {}, items: [], page: { limit: 25, offset: 0, total: 0 } };
      }
      throw new Error(`unexpected path ${path}`);
    });
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-pascal')).toBeInTheDocument();
    expect(screen.getByText('plan_limit_exceeded')).toBeInTheDocument();
  });

  it('refreshes all keys for one provider', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    fireEvent.click(screen.getByRole('button', { name: /refresh all tavily keys/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-key-quotas/tavily/refresh-all', { method: 'POST' });
    });
  });
});
