import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { DebugAttemptsPage } from './DebugAttemptsPage';
import * as client from '../../api/client';

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return { ...actual, apiFetch: vi.fn() };
});

function renderWithClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe('DebugAttemptsPage', () => {
  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/settings/proxy-debug-attempts') return { enabled: true };
      if (path === '/admin/api/proxy-attempts?limit=50&offset=0') {
        return {
          items: [
            { id: 1, request_id: 'req-t', provider: 'tavily', route_family: 'tavily', path: '/tavily/extract', attempt_index: 1, status_class: '429', reason: 'plan_limit_exceeded', terminal: false },
            { id: 2, request_id: 'req-g', provider: 'grok', route_family: 'grok', path: '/grok/v1/chat/completions', attempt_index: 1, status_class: '2xx', terminal: true },
          ],
          page: { limit: 50, offset: 0, total: 2 },
        };
      }
      throw new Error(`unexpected path ${path}`);
    });
  });

  it('defaults to all providers and filters client-side', async () => {
    renderWithClient(<DebugAttemptsPage />);
    expect(await screen.findByText('req-t')).toBeInTheDocument();
    expect(screen.getByText('req-g')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Tavily' }));
    expect(screen.getByText('req-t')).toBeInTheDocument();
    expect(screen.queryByText('req-g')).not.toBeInTheDocument();
  });

  it('toggles debug attempt logging', async () => {
    renderWithClient(<DebugAttemptsPage />);
    await screen.findByText(/Logging enabled/i);
    fireEvent.click(screen.getByRole('button', { name: /disable attempt logging/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/settings/proxy-debug-attempts', {
        method: 'PATCH',
        body: JSON.stringify({ enabled: false }),
      });
    });
  });
});
