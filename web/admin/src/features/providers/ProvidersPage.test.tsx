import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { isEnabledPoolRow, ProvidersPage } from './ProvidersPage';
import { AdminLayout } from '../../routes/AdminLayout';
import * as client from '../../api/client';
import type { ProviderPoolRow } from '../../api/types';

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return { ...actual, apiFetch: vi.fn() };
});

vi.mock('../../api/session', () => ({
  useSession: () => ({
    authenticated: true,
    loading: false,
    login: vi.fn(),
    logout: vi.fn(),
    reset: vi.fn(),
  }),
  SessionProvider: ({ children }: { children: React.ReactNode }) => children,
}));

function renderWithClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
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

function defaultMock(path: string) {
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
          key: {
            id: 5,
            provider: 'tavily',
            name: 'tavily-1',
            fingerprint: '1c105c',
            enabled: true,
            cooldown_reason: 'plan_limit_exceeded',
            last_failed_at: '2026-07-09T00:00:00Z',
            quota_mode: 'endpoint_credentials',
          },
          quota: { provider_key_id: 5, provider: 'tavily', available: false, source: 'tavily_usage', checked_at: '2026-07-09T00:00:00Z' },
        },
        {
          status: 'available',
          key: {
            id: 6,
            provider: 'tavily',
            name: 'tavily-2',
            fingerprint: 'bce42f',
            enabled: true,
            quota_mode: 'endpoint_credentials',
          },
          quota: {
            provider_key_id: 6,
            provider: 'tavily',
            available: true,
            source: 'tavily_usage',
            remaining: 427,
            limit_value: 1000,
            checked_at: '2026-07-09T00:01:00Z',
          },
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
    return {
      provider: 'tavily',
      attempted: 2,
      succeeded: 1,
      failed: 1,
      skipped_disabled: 1,
      skipped_not_configured: 0,
    };
  }
  if (path === '/admin/api/provider-quotas/tavily/refresh') {
    return { provider: 'tavily', available: true, source: 'tavily_usage' };
  }
  throw new Error(`unexpected path ${path}`);
}

describe('isEnabledPoolRow', () => {
  it('keeps available cooling and not_refreshed; drops disabled and archived', () => {
    const base = { key: { id: 1, name: 'x' } } as ProviderPoolRow;
    expect(isEnabledPoolRow({ ...base, status: 'available' })).toBe(true);
    expect(isEnabledPoolRow({ ...base, status: 'cooling' })).toBe(true);
    expect(isEnabledPoolRow({ ...base, status: 'not_refreshed' })).toBe(true);
    expect(isEnabledPoolRow({ ...base, status: 'disabled' })).toBe(false);
    expect(isEnabledPoolRow({ ...base, status: 'archived' })).toBe(false);
  });
});

describe('Provider Monitoring page', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => defaultMock(path));
  });

  it('renames Providers navigation and heading to Provider Monitoring', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/providers']}>
          <AdminLayout />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.getByRole('link', { name: /provider monitoring/i })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^providers$/i })).not.toBeInTheDocument();

    renderWithClient(<ProvidersPage />);
    expect(await screen.findByRole('heading', { name: 'Provider Monitoring' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: /^providers$/i })).not.toBeInTheDocument();
  });

  it('links to Manage Provider Endpoints', async () => {
    renderWithClient(<ProvidersPage />);
    const link = await screen.findByRole('link', { name: /manage provider endpoints/i });
    expect(link).toHaveAttribute('href', '/provider-keys');
  });

  it('does not render provider creation-default forms', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByRole('heading', { name: 'Provider Monitoring' });
    expect(screen.queryByText(/default url for new endpoints/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/default url for new endpoints/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/^settings$/i)).not.toBeInTheDocument();
  });

  it('shows inference, pool order, and quota as separate columns', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    // Three provider pools each render the same headers.
    expect(screen.getAllByRole('columnheader', { name: /endpoint/i }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('columnheader', { name: /inference/i }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('columnheader', { name: /cooldown/i }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('columnheader', { name: /pool order/i }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('columnheader', { name: /^quota$/i }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('columnheader', { name: /checked/i }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('columnheader', { name: /actions/i }).length).toBeGreaterThan(0);
  });

  it('shows disabled and not configured quota states', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/grok?limit=25&offset=0') {
        return {
          provider: 'grok',
          summary: {
            provider: 'grok',
            key_count: 2,
            enabled_key_count: 2,
            available_key_count: 2,
            cooling_key_count: 0,
            refreshed_key_count: 1,
          },
          items: [
            {
              status: 'available',
              key: {
                id: 10,
                provider: 'grok',
                name: 'grok-disabled-quota',
                enabled: true,
                quota_mode: 'disabled',
              },
              quota: {
                provider_key_id: 10,
                provider: 'grok',
                available: false,
                source: 'quota_disabled',
                checked_at: '2026-07-09T00:00:00Z',
                message_redacted: 'quota refresh disabled for this endpoint',
              },
            },
            {
              status: 'available',
              key: {
                id: 11,
                provider: 'grok',
                name: 'grok-not-configured',
                enabled: true,
                quota_mode: 'separate_credentials',
                quota_key_configured: false,
              },
              quota: {
                provider_key_id: 11,
                provider: 'grok',
                available: false,
                source: 'quota_not_configured',
                checked_at: '2026-07-09T00:00:00Z',
                message_redacted: 'quota credentials not configured for this endpoint',
              },
            },
          ],
          page: { limit: 25, offset: 0, total: 2 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });

    renderWithClient(<ProvidersPage />);
    await screen.findByText('Grok Pool');
    expect(screen.getByText('grok-disabled-quota')).toBeInTheDocument();
    expect(screen.getAllByText(/^disabled$/i).length).toBeGreaterThan(0);
    expect(screen.getByText('grok-not-configured')).toBeInTheDocument();
    expect(screen.getAllByText(/not configured/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/configure quota credentials/i)).toBeInTheDocument();

    // Inference remains available even when quota is disabled/not configured.
    const disabledRow = screen.getByText('grok-disabled-quota').closest('tr')!;
    expect(within(disabledRow).getByText('available')).toBeInTheDocument();
    expect(within(disabledRow).getAllByText(/^disabled$/i).length).toBeGreaterThan(0);
  });

  it('defaults pool view to enabled endpoints and can show disabled/archived via Show all', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: {
            provider: 'tavily',
            key_count: 4,
            enabled_key_count: 2,
            available_key_count: 1,
            cooling_key_count: 1,
            refreshed_key_count: 2,
          },
          items: [
            {
              status: 'available',
              key: { id: 1, provider: 'tavily', name: 'tavily-live', enabled: true, quota_mode: 'endpoint_credentials' },
              quota: {
                provider_key_id: 1,
                provider: 'tavily',
                available: true,
                source: 'tavily_usage',
                remaining: 10,
                limit_value: 100,
                checked_at: '2026-07-11T00:00:00Z',
              },
            },
            {
              status: 'cooling',
              key: {
                id: 2,
                provider: 'tavily',
                name: 'tavily-cool',
                enabled: true,
                cooldown_reason: 'rate_limited',
                quota_mode: 'endpoint_credentials',
              },
            },
            {
              status: 'disabled',
              key: { id: 3, provider: 'tavily', name: 'tavily-off', enabled: false, quota_mode: 'endpoint_credentials' },
            },
            {
              status: 'archived',
              key: {
                id: 4,
                provider: 'tavily',
                name: 'tavily-old',
                enabled: false,
                archived_at: '2026-07-01T00:00:00Z',
                quota_mode: 'endpoint_credentials',
              },
            },
          ],
          page: { limit: 25, offset: 0, total: 4 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });

    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');

    // Default: enabled only — live + cooling stay; disabled/archived hidden.
    expect(screen.getByText('tavily-live')).toBeInTheDocument();
    expect(screen.getByText('tavily-cool')).toBeInTheDocument();
    expect(screen.queryByText('tavily-off')).not.toBeInTheDocument();
    expect(screen.queryByText('tavily-old')).not.toBeInTheDocument();
    expect(screen.getByTestId('pool-view-hint-tavily')).toHaveTextContent(/hiding 2 disabled\/archived/i);

    const enabledOnly = screen.getByRole('button', { name: /show enabled tavily endpoints only/i });
    const showAll = screen.getByRole('button', { name: /show all tavily endpoints/i });
    expect(enabledOnly).toHaveAttribute('aria-pressed', 'true');
    expect(showAll).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(showAll);
    expect(await screen.findByText('tavily-off')).toBeInTheDocument();
    expect(screen.getByText('tavily-old')).toBeInTheDocument();
    expect(showAll).toHaveAttribute('aria-pressed', 'true');
    expect(enabledOnly).toHaveAttribute('aria-pressed', 'false');
    expect(screen.queryByTestId('pool-view-hint-tavily')).not.toBeInTheDocument();

    fireEvent.click(enabledOnly);
    await waitFor(() => {
      expect(screen.queryByText('tavily-off')).not.toBeInTheDocument();
    });
    expect(screen.queryByText('tavily-old')).not.toBeInTheDocument();
  });

  it('keeps provider tests, quota refresh, promote, demote, and reset controls', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    expect(screen.getByRole('button', { name: /select key/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /refresh all tavily keys/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /refresh sample for tavily/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /refresh key 6/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /promote key 5/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /demote key 6/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /reset cool and order for key 6/i })).toBeInTheDocument();
  });

  it('does not render URL, key, enable, archive, restore, delete, or create controls', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    expect(screen.queryByRole('button', { name: /add endpoint/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^enable$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^disable$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^archive$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^restore$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^delete$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^edit$/i })).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^base url$/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^key$/i)).not.toBeInTheDocument();
  });

  it('reports refreshed failed and skipped-disabled refresh-all counts', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    fireEvent.click(screen.getByRole('button', { name: /refresh all tavily keys/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-key-quotas/tavily/refresh-all', { method: 'POST' });
    });
    expect(await screen.findByText(/Refreshed 1/)).toBeInTheDocument();
    expect(screen.getByText(/Failed 1/)).toBeInTheDocument();
    expect(screen.getByText(/Skipped disabled 1/)).toBeInTheDocument();
  });

  it('disables refresh-one when quota is disabled', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/grok?limit=25&offset=0') {
        return {
          provider: 'grok',
          summary: {
            provider: 'grok',
            key_count: 1,
            enabled_key_count: 1,
            available_key_count: 1,
            cooling_key_count: 0,
            refreshed_key_count: 1,
          },
          items: [
            {
              status: 'available',
              key: { id: 10, provider: 'grok', name: 'grok-off', enabled: true, quota_mode: 'disabled' },
              quota: {
                provider_key_id: 10,
                provider: 'grok',
                available: false,
                source: 'quota_disabled',
                checked_at: '2026-07-09T00:00:00Z',
              },
            },
          ],
          page: { limit: 25, offset: 0, total: 1 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });
    renderWithClient(<ProvidersPage />);
    await screen.findByText('grok-off');
    expect(screen.getByRole('button', { name: /refresh key 10/i })).toBeDisabled();
  });

  it('shows provider pool summary and paginated key rows', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('Tavily Pool')).toBeInTheDocument();
    expect(screen.getByText(/Enabled 3/)).toBeInTheDocument();
    expect(screen.getByText('tavily-1')).toBeInTheDocument();
    expect(screen.getByText('plan_limit_exceeded')).toBeInTheDocument();
    expect(screen.getByText('427 / 1000 remaining')).toBeInTheDocument();
  });

  it('does not show provider-wide quota errors when pool key quotas are refreshed', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health') return { items: [] };
      if (path === '/admin/api/provider-quotas') {
        return {
          items: [
            {
              provider: 'grok',
              available: false,
              source: 'unsupported',
              checked_at: '2026-07-09T00:00:00Z',
              expires_at: '2026-07-09T00:05:00Z',
              message_redacted: 'upstream quota not available',
            },
          ],
        };
      }
      if (path === '/admin/api/provider-pools/grok?limit=25&offset=0') {
        return {
          provider: 'grok',
          summary: {
            provider: 'grok',
            key_count: 1,
            enabled_key_count: 1,
            available_key_count: 1,
            cooling_key_count: 0,
            refreshed_key_count: 1,
            known_remaining: 310018,
          },
          items: [
            {
              status: 'available',
              key: {
                id: 7,
                provider: 'grok',
                name: 'grok2api',
                fingerprint: 'aabbcc',
                enabled: true,
                quota_mode: 'separate_credentials',
                quota_key_configured: true,
              },
              quota: {
                provider_key_id: 7,
                provider: 'grok',
                available: true,
                source: 'grok2api_admin_tokens',
                remaining: 310018,
                limit_value: 364150,
                checked_at: '2026-07-09T10:14:07Z',
              },
            },
          ],
          page: { limit: 25, offset: 0, total: 1 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });

    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('Grok Pool')).toBeInTheDocument();
    expect(screen.getByText(/KnownRemaining 310018/)).toBeInTheDocument();
    expect(screen.getByText('310018 / 364150 remaining')).toBeInTheDocument();
    expect(screen.queryByText('upstream quota not available')).not.toBeInTheDocument();
  });

  it('shows usage not "not refreshed" when quota row exists but remaining is null', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: {
            provider: 'tavily',
            key_count: 1,
            enabled_key_count: 1,
            available_key_count: 1,
            cooling_key_count: 0,
            refreshed_key_count: 1,
          },
          items: [
            {
              status: 'available',
              key: {
                id: 6,
                provider: 'tavily',
                name: 'tavily-2',
                fingerprint: 'bce42f',
                enabled: true,
                quota_mode: 'endpoint_credentials',
              },
              quota: {
                provider_key_id: 6,
                provider: 'tavily',
                available: true,
                source: 'tavily_usage',
                used: 1,
                checked_at: '2026-07-09T00:01:00Z',
              },
            },
          ],
          page: { limit: 25, offset: 0, total: 1 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });
    renderWithClient(<ProvidersPage />);
    await screen.findByText('Tavily Pool');
    expect(screen.queryByText('not refreshed')).not.toBeInTheDocument();
    expect(screen.getByText(/used 1/i)).toBeInTheDocument();
  });

  it('reads cooldown reason from PascalCase Go JSON', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path === '/admin/api/provider-pools/tavily?limit=25&offset=0') {
        return {
          provider: 'tavily',
          summary: {
            provider: 'tavily',
            key_count: 1,
            enabled_key_count: 1,
            available_key_count: 0,
            cooling_key_count: 1,
            refreshed_key_count: 0,
          },
          items: [
            {
              status: 'cooling',
              key: {
                ID: 9,
                Provider: 'tavily',
                Name: 'tavily-pascal',
                Fingerprint: 'ff00ff',
                Enabled: true,
                CooldownReason: 'plan_limit_exceeded',
                QuotaMode: 'endpoint_credentials',
              },
              quota: null,
            },
          ],
          page: { limit: 25, offset: 0, total: 1 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
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
