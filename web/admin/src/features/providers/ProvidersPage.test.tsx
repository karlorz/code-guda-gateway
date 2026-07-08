import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
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

describe('ProvidersPage quotas', () => {
  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings') {
        return { items: [{ provider: 'tavily', base_url: 'https://api.tavily.com' }] };
      }
      if (path === '/admin/api/provider-health') {
        return { items: [{ provider: 'tavily', status: 'healthy', key_count: 1, enabled_key_count: 1, cooldown_key_count: 0, reasons: [] }] };
      }
      if (path === '/admin/api/provider-keys') {
        return {
          items: [
            { id: 2, provider: 'tavily', name: 'gh01', enabled: true },
            { id: 3, provider: 'tavily', name: 'gh02', enabled: true },
            { id: 4, provider: 'tavily', name: 'gmail', enabled: true },
          ],
        };
      }
      if (path === '/admin/api/provider-quotas') {
        return {
          items: [
            {
              provider: 'tavily',
              provider_key_id: 2,
              available: true,
              source: 'tavily_usage',
              remaining: 850,
              limit_value: 1000,
              used: 150,
              checked_at: '2026-07-08T12:00:00.000Z',
            },
          ],
        };
      }
      if (path === '/admin/api/provider-quotas/tavily/refresh') {
        return { provider: 'tavily', available: true, source: 'tavily_usage' };
      }
      throw new Error(`unexpected path ${path}`);
    });
  });

  it('shows available quota remaining and limit', async () => {
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('850 / 1000 remaining')).toBeInTheDocument();
    expect(screen.getByText(/Source: tavily_usage/)).toBeInTheDocument();
    expect(screen.getByText('Quota key: gh01 (#2)')).toBeInTheDocument();
    expect(screen.getByText('Quota refresh uses 1 of 3 enabled keys.')).toBeInTheDocument();
    expect(screen.getByText(/Checked:/)).toBeInTheDocument();
  });

  it('shows Firecrawl plan plus one-time credits without bogus used', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings') return { items: [] };
      if (path === '/admin/api/provider-health') return { items: [] };
      if (path === '/admin/api/provider-keys') {
        return { items: [{ id: 7, provider: 'firecrawl', name: 'gh01', enabled: true }] };
      }
      if (path === '/admin/api/provider-quotas') {
        return {
          items: [
            {
              provider: 'firecrawl',
              provider_key_id: 7,
              available: true,
              source: 'firecrawl_credit_usage',
              remaining: 1400,
              used: 0,
              details: { plan_credits: 1000, extra_credits_remaining: 400 },
              checked_at: '2026-07-08T18:42:05.671803Z',
            },
          ],
        };
      }
      throw new Error(`unexpected path ${path}`);
    });
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('1400 credits remaining (1000 plan + 400 one-time)')).toBeInTheDocument();
    expect(screen.getByText('Used: 0')).toBeInTheDocument();
  });

  it('shows unavailable message when quota not available', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/provider-settings') return { items: [] };
      if (path === '/admin/api/provider-health') return { items: [] };
      if (path === '/admin/api/provider-keys') return { items: [] };
      if (path === '/admin/api/provider-quotas') {
        return {
          items: [
            {
              provider: 'grok',
              available: false,
              source: 'grok2api_admin_required',
              message_redacted: 'grok2api admin key required for quota refresh',
            },
          ],
        };
      }
      return {};
    });
    renderWithClient(<ProvidersPage />);
    expect(await screen.findByText('grok2api admin key required for quota refresh')).toBeInTheDocument();
    expect(screen.getByText('not available')).toBeInTheDocument();
  });

  it('calls refresh endpoint when Refresh is clicked', async () => {
    renderWithClient(<ProvidersPage />);
    await screen.findByText('850 / 1000 remaining');
    fireEvent.click(screen.getByRole('button', { name: /refresh quota for tavily/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-quotas/tavily/refresh', { method: 'POST' });
    });
  });
});
