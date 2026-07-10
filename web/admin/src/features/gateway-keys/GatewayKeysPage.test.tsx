import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { GatewayKeysPage } from './GatewayKeysPage';
import * as client from '../../api/client';

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return { ...actual, apiFetch: vi.fn() };
});

function renderWithClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

const listItem = {
  ID: 7,
  Name: 'ops',
  Prefix: 'gsk_',
  Fingerprint: 'abc123',
  Enabled: true,
};

describe('GatewayKeysPage mutations', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      const method = init?.method;
      if (path === '/admin/api/gateway-keys' && !method) {
        return { items: [listItem], page: { limit: 25, offset: 0 } };
      }
      if (path.startsWith('/admin/api/gateway-keys/')) {
        return { status: 'ok' };
      }
      throw new Error(`unexpected ${path}`);
    });
  });

  it('calls PATCH for disable and POST for revoke', async () => {
    renderWithClient(<GatewayKeysPage />);
    await screen.findByText('ops');
    fireEvent.click(screen.getByRole('button', { name: /Disable/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/gateway-keys/7', {
        method: 'PATCH',
        body: JSON.stringify({ enabled: false }),
      });
    });
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/gateway-keys/7/revoke', { method: 'POST', body: undefined });
    });
  });

  it('calls PATCH enable when row is disabled', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      const method = init?.method;
      if (path === '/admin/api/gateway-keys' && !method) {
        return { items: [{ ...listItem, Enabled: false }], page: { limit: 25, offset: 0 } };
      }
      if (path.startsWith('/admin/api/gateway-keys/')) {
        return { status: 'ok' };
      }
      throw new Error(`unexpected ${path}`);
    });
    renderWithClient(<GatewayKeysPage />);
    await screen.findByRole('button', { name: /Enable/i });
    fireEvent.click(screen.getByRole('button', { name: /Enable/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/gateway-keys/7', {
        method: 'PATCH',
        body: JSON.stringify({ enabled: true }),
      });
    });
  });
});