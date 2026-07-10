import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { ProviderKeysPage } from './ProviderKeysPage';
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
  ID: 42,
  Provider: 'grok',
  Name: 'primary',
  KeyPrefix: 'xai-',
  Enabled: true,
};

describe('ProviderKeysPage mutations', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      const method = init?.method;
      if (path === '/admin/api/provider-keys' && !method) {
        return { items: [listItem], page: { limit: 25, offset: 0 } };
      }
      if (path === '/admin/api/provider-keys' && method === 'POST') {
        return listItem;
      }
      if (path.startsWith('/admin/api/provider-keys/')) {
        return { status: 'ok' };
      }
      throw new Error(`unexpected ${path} ${method ?? 'GET'}`);
    });
  });

  it('calls PATCH enable/disable with correct body', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Disable' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-keys/42', {
        method: 'PATCH',
        body: JSON.stringify({ enabled: false }),
      });
    });
  });

  it('calls POST reset-cooldown and archive', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Reset cooldown' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-keys/42/reset-cooldown', { method: 'POST', body: undefined });
    });
    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-keys/42/archive', { method: 'POST', body: undefined });
    });
  });

  it('calls POST restore for archived row', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      const method = init?.method;
      if (path === '/admin/api/provider-keys' && !method) {
        return { items: [{ ...listItem, archived_at: '2026-01-01T00:00:00Z' }], page: { limit: 25, offset: 0 } };
      }
      if (path.startsWith('/admin/api/provider-keys/')) {
        return { status: 'ok' };
      }
      throw new Error(`unexpected ${path}`);
    });
    renderWithClient(<ProviderKeysPage />);
    await screen.findByRole('button', { name: 'Restore' });
    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-keys/42/restore', { method: 'POST', body: undefined });
    });
  });

  it('does not crash when action mutation fails', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      const method = init?.method;
      if (path === '/admin/api/provider-keys' && !method) {
        return { items: [listItem], page: { limit: 25, offset: 0 } };
      }
      if (path.startsWith('/admin/api/provider-keys/')) {
        throw new Error('network');
      }
      throw new Error(`unexpected ${path}`);
    });
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Disable' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalled();
    });
    expect(screen.getByText('primary')).toBeInTheDocument();
  });
});