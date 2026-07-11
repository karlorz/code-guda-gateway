import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as client from '../../api/client';
import { OverviewPage } from './OverviewPage';

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return { ...actual, apiFetch: vi.fn() };
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><OverviewPage /></MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('OverviewPage', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/gateway-keys') return { items: [{ id: 1, name: 'ops' }] };
      if (path === '/admin/api/provider-health') {
        return {
          items: [
            { provider: 'grok', status: 'healthy', key_count: 2, enabled_key_count: 2, cooldown_key_count: 1, reasons: [] },
            { provider: 'tavily', status: 'degraded', key_count: 3, enabled_key_count: 2, cooldown_key_count: 0, reasons: ['one endpoint disabled'] },
            { provider: 'firecrawl', status: 'healthy', key_count: 1, enabled_key_count: 1, cooldown_key_count: 0, reasons: [] },
          ],
        };
      }
      if (path === '/admin/api/audit-events?limit=5') return { items: [] };
      throw new Error(`unexpected path ${path}`);
    });
  });

  it('shows readiness totals and endpoint terminology', async () => {
    renderPage();
    expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId('overview-gateway-keys')).toHaveTextContent('1');
    });
    expect(screen.getByTestId('overview-providers-ready')).toHaveTextContent('2/3');
    expect(screen.getByTestId('overview-active-endpoints')).toHaveTextContent('5');
    expect(screen.getByTestId('overview-cooling-endpoints')).toHaveTextContent('1');
    expect(screen.getByText('Provider endpoints configured')).toBeInTheDocument();
    expect(screen.queryByText(/^Provider keys$/)).not.toBeInTheDocument();
  });

  it('does not duplicate provider quota or pool-order details', async () => {
    renderPage();
    await screen.findByRole('heading', { name: 'Overview' });
    expect(screen.queryByText(/known remaining/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/pool order/i)).not.toBeInTheDocument();
  });
});
