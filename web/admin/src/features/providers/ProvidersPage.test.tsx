import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { ProvidersPage } from './ProvidersPage';
import { AdminLayout } from '../../routes/AdminLayout';
import * as client from '../../api/client';

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
    return {
      items: [
        { provider: 'grok', status: 'healthy', key_count: 2, enabled_key_count: 2, cooldown_key_count: 1, reasons: [] },
        { provider: 'tavily', status: 'degraded', key_count: 3, enabled_key_count: 2, cooldown_key_count: 1, reasons: ['one endpoint disabled'] },
        { provider: 'firecrawl', status: 'healthy', key_count: 1, enabled_key_count: 1, cooldown_key_count: 0, reasons: [] },
      ],
    };
  }
  if (path === '/admin/api/provider-quotas') {
    return { items: [] };
  }
  if (path.startsWith('/admin/api/provider-pools/tavily?') && path.includes('offset=0')) {
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

  it('renders one compact health card for each provider', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByRole('heading', { name: 'Provider Monitoring' });
    expect(await screen.findByTestId('provider-health-grok')).toHaveTextContent('2/2 active');
    expect(screen.getByTestId('provider-health-tavily')).toHaveTextContent('2/3 active');
    expect(screen.getByTestId('provider-health-tavily')).toHaveTextContent('one endpoint disabled');
    expect(screen.getByTestId('provider-health-firecrawl')).toHaveTextContent('1/1 active');
  });

  it('renders the full-pool metric strip and omits unknown remaining', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-1')).toBeInTheDocument();
    const summary = screen.getByTestId('pool-summary-tavily');
    expect(within(summary).getByText('Enabled')).toBeInTheDocument();
    expect(within(summary).getByTestId('pool-summary-tavily-enabled')).toHaveTextContent('3');
    expect(within(summary).getByTestId('pool-summary-tavily-available')).toHaveTextContent('2');
    expect(within(summary).getByTestId('pool-summary-tavily-cooling')).toHaveTextContent('1');
    expect(within(summary).getByTestId('pool-summary-tavily-refreshed')).toHaveTextContent('2');
    expect(within(summary).getByTestId('pool-summary-tavily-remaining')).toHaveTextContent('427');

    const grokSummary = screen.getByTestId('pool-summary-grok');
    expect(within(grokSummary).queryByText('Known remaining')).not.toBeInTheDocument();
  });

  it('uses explicit quota language for pool actions', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByRole('button', { name: /refresh quota sample for tavily/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /refresh all tavily endpoint quotas/i })).toBeInTheDocument();
  });

  it('shows inference, pool order, and quota as separate columns', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-1')).toBeInTheDocument();
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
      if (path.startsWith('/admin/api/provider-pools/grok?') && path.includes('offset=0')) {
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
    expect(await screen.findByText('grok-disabled-quota')).toBeInTheDocument();
    expect(screen.getAllByText(/^disabled$/i).length).toBeGreaterThan(0);
    expect(screen.getByText('grok-not-configured')).toBeInTheDocument();
    expect(screen.getAllByText(/not configured/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/configure quota credentials/i)).toBeInTheDocument();

    // Inference remains available even when quota is disabled/not configured.
    const disabledRow = screen.getByText('grok-disabled-quota').closest('tr')!;
    expect(within(disabledRow).getByText('available')).toBeInTheDocument();
    expect(within(disabledRow).getAllByText(/^disabled$/i).length).toBeGreaterThan(0);
  });

  it('defaults to Active pool and refetches All endpoints from offset zero', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path.startsWith('/admin/api/provider-pools/tavily?')) {
        const viewAll = path.includes('view=all');
        const items = [
          {
            status: 'available' as const,
            key: { id: 1, provider: 'tavily', name: 'tavily-live', enabled: true, quota_mode: 'endpoint_credentials' as const },
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
            status: 'cooling' as const,
            key: {
              id: 2,
              provider: 'tavily',
              name: 'tavily-cool',
              enabled: true,
              cooldown_reason: 'rate_limited',
              quota_mode: 'endpoint_credentials' as const,
            },
          },
        ];
        if (viewAll) {
          items.push(
            {
              status: 'disabled' as const,
              key: { id: 3, provider: 'tavily', name: 'tavily-off', enabled: false, quota_mode: 'endpoint_credentials' as const },
            } as any,
            {
              status: 'archived' as const,
              key: {
                id: 4,
                provider: 'tavily',
                name: 'tavily-old',
                enabled: false,
                archived_at: '2026-07-01T00:00:00Z',
                quota_mode: 'endpoint_credentials' as const,
              },
            } as any,
          );
        }
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
          items: viewAll ? items : items.filter((r) => r.status === 'available' || r.status === 'cooling'),
          page: { limit: 25, offset: 0, total: viewAll ? 4 : 2 },
        };
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });

    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-live')).toBeInTheDocument();

    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
        expect.stringMatching(/\/admin\/api\/provider-pools\/tavily\?.*view=enabled/),
      );
    });

    const activeChip = screen.getByRole('button', { name: /show active tavily pool/i });
    const allChip = screen.getByRole('button', { name: /show all tavily endpoints/i });
    expect(activeChip).toHaveAttribute('aria-pressed', 'true');
    expect(activeChip).toHaveTextContent('Active pool');
    expect(activeChip).toHaveTextContent('2');
    expect(allChip).toHaveAttribute('aria-pressed', 'false');
    expect(allChip).toHaveTextContent('All endpoints');
    expect(allChip).toHaveTextContent('4');
    expect(screen.getByTestId('pool-view-hint-tavily')).toHaveTextContent(
      'Active pool includes available and cooling endpoints. Disabled and archived endpoints appear under All endpoints.',
    );

    expect(screen.getByText('tavily-cool')).toBeInTheDocument();
    expect(screen.queryByText('tavily-off')).not.toBeInTheDocument();

    fireEvent.click(allChip);

    await waitFor(() => expect(allChip).toHaveAttribute('aria-pressed', 'true'));
    expect(screen.queryByTestId('pool-view-hint-tavily')).not.toBeInTheDocument();

    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
        expect.stringMatching(/\/admin\/api\/provider-pools\/tavily\?.*view=all/),
      );
    });
    // Toggle resets pagination to first page (offset=0).
    expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
      expect.stringMatching(/\/admin\/api\/provider-pools\/tavily\?.*offset=0.*view=all|view=all.*offset=0/),
    );
    expect(await screen.findByText('tavily-off')).toBeInTheDocument();
    expect(screen.getByText('tavily-old')).toBeInTheDocument();
  });

  it('keeps pool title and summary mounted while view refetches', async () => {
    let resolveAll: ((value: unknown) => void) | undefined;
    const allGate = new Promise((resolve) => {
      resolveAll = resolve;
    });
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path.startsWith('/admin/api/provider-pools/tavily?')) {
        const payload = {
          provider: 'tavily',
          summary: {
            provider: 'tavily',
            key_count: 3,
            enabled_key_count: 2,
            available_key_count: 1,
            cooling_key_count: 1,
            refreshed_key_count: 1,
          },
          items: path.includes('view=all')
            ? [
                {
                  status: 'available',
                  key: { id: 1, provider: 'tavily', name: 'tavily-live', enabled: true, quota_mode: 'endpoint_credentials' },
                },
                {
                  status: 'disabled',
                  key: { id: 2, provider: 'tavily', name: 'tavily-off', enabled: false, quota_mode: 'endpoint_credentials' },
                },
              ]
            : [
                {
                  status: 'available',
                  key: { id: 1, provider: 'tavily', name: 'tavily-live', enabled: true, quota_mode: 'endpoint_credentials' },
                },
              ],
          page: { limit: 25, offset: 0, total: path.includes('view=all') ? 2 : 1 },
        };
        if (path.includes('view=all')) {
          await allGate;
        }
        return payload;
      }
      if (path.startsWith('/admin/api/provider-pools/')) {
        return emptyPool(path.split('/')[4].split('?')[0]);
      }
      throw new Error(`unexpected path ${path}`);
    });

    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-live')).toBeInTheDocument();
    expect(screen.getByText('Tavily Pool')).toBeInTheDocument();
    expect(screen.getByTestId('pool-summary-tavily-enabled')).toHaveTextContent('2');

    fireEvent.click(screen.getByRole('button', { name: /show all tavily endpoints/i }));
    // While view=all is in flight, chrome stays mounted (keepPreviousData).
    expect(screen.getByText('Tavily Pool')).toBeInTheDocument();
    expect(screen.getByTestId('pool-summary-tavily')).toBeInTheDocument();
    expect(screen.getByTestId('pool-summary-tavily-enabled')).toHaveTextContent('2');
    expect(screen.getByText('tavily-live')).toBeInTheDocument();

    resolveAll?.(undefined);
    expect(await screen.findByText('tavily-off')).toBeInTheDocument();
    expect(screen.getByText('Tavily Pool')).toBeInTheDocument();
  });

  it('keeps quota refresh visible and exposes endpoint-specific secondary selection actions', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('tavily-2');

    expect(screen.getByRole('button', { name: /refresh quota for tavily-2/i })).toBeInTheDocument();

    const row = screen.getByText('tavily-2').closest('tr')!;
    expect(within(row).getByRole('button', { name: /promote tavily-2 in pool/i })).toBeInTheDocument();
    expect(within(row).getByRole('button', { name: /demote tavily-2 in pool/i })).toBeInTheDocument();
    expect(within(row).getByRole('button', { name: /reset cooldown and pool order for tavily-2/i })).toBeInTheDocument();
    expect(within(row).queryByText(/more actions/i)).not.toBeInTheDocument();
  });

  it('does not render URL, key, enable, archive, restore, delete, or create controls', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-1')).toBeInTheDocument();
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
    expect(await screen.findByRole('button', { name: /refresh all tavily endpoint quotas/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /refresh all tavily endpoint quotas/i }));
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
      if (path.startsWith('/admin/api/provider-pools/grok?') && path.includes('offset=0')) {
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
    expect(screen.getByRole('button', { name: /refresh quota for grok-off/i })).toBeDisabled();
  });

  it('shows provider pool summary and paginated key rows', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('tavily-1')).toBeInTheDocument();
    expect(screen.getByTestId('pool-summary-tavily-enabled')).toHaveTextContent('3');
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
      if (path.startsWith('/admin/api/provider-pools/grok?') && path.includes('offset=0')) {
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
    expect(await screen.findByText('grok2api')).toBeInTheDocument();
    expect(screen.getByTestId('pool-summary-grok-remaining')).toHaveTextContent('310018');
    expect(screen.getByText('310018 / 364150 remaining')).toBeInTheDocument();
    expect(screen.queryByText('upstream quota not available')).not.toBeInTheDocument();
  });

  it('shows usage not "not refreshed" when quota row exists but remaining is null', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path.startsWith('/admin/api/provider-pools/tavily?') && path.includes('offset=0')) {
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
    expect(await screen.findByText(/used 1/i)).toBeInTheDocument();
    expect(screen.queryByText('not refreshed')).not.toBeInTheDocument();
  });

  it('reads cooldown reason from PascalCase Go JSON', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-health' || path === '/admin/api/provider-quotas') return { items: [] };
      if (path.startsWith('/admin/api/provider-pools/tavily?') && path.includes('offset=0')) {
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
    expect(await screen.findByRole('button', { name: /refresh all tavily endpoint quotas/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /refresh all tavily endpoint quotas/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-key-quotas/tavily/refresh-all', { method: 'POST' });
    });
  });
});
