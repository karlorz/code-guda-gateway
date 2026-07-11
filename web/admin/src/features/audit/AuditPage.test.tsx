import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import * as client from '../../api/client';
import { AuditPage } from './AuditPage';

vi.mock('../../api/client', () => ({ apiFetch: vi.fn() }));

describe('AuditPage', () => {
  afterEach(() => {
    cleanup();
  });

  it('localizes occurred_at using display timezone', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/settings/display-timezone') {
        return { timezone: 'Asia/Seoul', source: 'stored' };
      }
      if (path.startsWith('/admin/api/audit-events')) {
        return {
          items: [{
            id: 1,
            occurred_at: '2026-07-12T12:00:00.000Z',
            actor_kind: 'admin',
            action: 'settings.patch',
            detail_redacted: 'ok',
          }],
        };
      }
      return {};
    });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <AuditPage />
      </QueryClientProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText(/21:00:00/)).toBeInTheDocument();
    });
    expect(screen.queryByText('2026-07-12T12:00:00.000Z')).not.toBeInTheDocument();
  });
});
