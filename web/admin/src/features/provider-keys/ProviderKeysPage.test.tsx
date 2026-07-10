import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { ProviderKeysPage } from './ProviderKeysPage';
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
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

const listItem = {
  ID: 42,
  Provider: 'grok',
  Name: 'primary',
  KeyPrefix: 'xai-',
  Enabled: true,
  BaseURL: 'https://api.x.ai/v1',
};

const listItemAlt = {
  ID: 43,
  Provider: 'tavily',
  Name: 'tv-proxy',
  KeyPrefix: 'tvly-',
  Enabled: true,
  BaseURL: 'https://proxy.example/tavily',
};

function mockDefaultEndpoints(items: unknown[] = [listItem, listItemAlt]) {
  vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
    const method = init?.method;
    if (path === '/admin/api/provider-endpoints' && !method) {
      return { items, page: { limit: 25, offset: 0 } };
    }
    if (path === '/admin/api/provider-settings' && !method) {
      return {
        items: [
          { provider: 'grok', base_url: 'https://api.x.ai/v1' },
          { provider: 'tavily', base_url: 'https://api.tavily.com' },
          { provider: 'firecrawl', base_url: 'https://api.firecrawl.dev' },
        ],
      };
    }
    if (path === '/admin/api/provider-endpoints' && method === 'POST') {
      return listItem;
    }
    if (path.startsWith('/admin/api/provider-endpoints/')) {
      return { status: 'ok' };
    }
    throw new Error(`unexpected ${path} ${method ?? 'GET'}`);
  });
}

describe('Provider Endpoints navigation and page', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockReset();
    mockDefaultEndpoints();
  });

  it('navigation label says Provider Endpoints', () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/provider-keys']}>
          <AdminLayout />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.getByRole('link', { name: /provider endpoints/i })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^provider keys$/i })).not.toBeInTheDocument();
  });

  it('page heading says Provider Endpoints', async () => {
    renderWithClient(<ProviderKeysPage />);
    expect(await screen.findByRole('heading', { name: 'Provider Endpoints' })).toBeInTheDocument();
  });

  it('create form submits base_url to canonical provider-endpoints', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');

    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: 'new-ep' } });
    fireEvent.change(screen.getByLabelText(/^base url$/i), { target: { value: 'https://custom.example/v1' } });
    const keyInput = screen.getByLabelText(/^key$/i);
    expect(keyInput).toHaveAttribute('type', 'password');
    fireEvent.change(keyInput, { target: { value: 'xai-secret-create-key-abcdef' } });

    fireEvent.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
        '/admin/api/provider-endpoints',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({
            provider: 'grok',
            name: 'new-ep',
            base_url: 'https://custom.example/v1',
            key: 'xai-secret-create-key-abcdef',
          }),
        }),
      );
    });
  });

  it('table shows mixed row-owned base URLs', async () => {
    renderWithClient(<ProviderKeysPage />);
    expect(await screen.findByText('https://api.x.ai/v1')).toBeInTheDocument();
    expect(screen.getByText('https://proxy.example/tavily')).toBeInTheDocument();
  });

  it('edit URL calls POST update-base-url', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');

    fireEvent.click(screen.getByRole('button', { name: /edit url/i }));
    const dialog = await screen.findByRole('dialog', { name: /edit base url/i });
    const urlField = within(dialog).getByLabelText(/base url/i);
    fireEvent.change(urlField, { target: { value: 'https://new-endpoint.example/v1' } });
    fireEvent.click(within(dialog).getByRole('button', { name: /save url/i }));

    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
        '/admin/api/provider-endpoints/42/update-base-url',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ base_url: 'https://new-endpoint.example/v1' }),
        }),
      );
    });
  });

  it('rotate key uses empty password input and never shows existing raw value', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');

    fireEvent.click(screen.getByRole('button', { name: /^rotate key$/i }));
    const dialog = await screen.findByRole('dialog', { name: /rotate key/i });
    const keyField = within(dialog).getByLabelText(/new key/i);
    expect(keyField).toHaveAttribute('type', 'password');
    expect(keyField).toHaveValue('');
    expect(within(dialog).queryByDisplayValue(/xai-|tvly-|secret/i)).not.toBeInTheDocument();

    fireEvent.change(keyField, { target: { value: 'xai-rotated-key-value-zzzz' } });
    fireEvent.click(within(dialog).getByRole('button', { name: /confirm rotate/i }));

    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
        '/admin/api/provider-endpoints/42/rotate-key',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ key: 'xai-rotated-key-value-zzzz' }),
        }),
      );
    });
  });

  it('keeps enable, reset cool+order, promote, demote, and archive controls', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset cool+order' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Promote' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Demote' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument();
  });

  it('calls PATCH enable/disable on canonical endpoint path', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Disable' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42', {
        method: 'PATCH',
        body: JSON.stringify({ enabled: false }),
      });
    });
  });

  it('calls POST reset-cooldown and archive on canonical endpoint paths', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Reset cool+order' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42/reset-cooldown', {
        method: 'POST',
        body: undefined,
      });
    });
    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42/archive', {
        method: 'POST',
        body: undefined,
      });
    });
  });

  it('calls POST demote and promote (reset-selection) on canonical paths', async () => {
    mockDefaultEndpoints([{ ...listItem, LastFailedAt: '2026-07-09T00:00:00Z' }]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Demote' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42/demote', {
        method: 'POST',
        body: undefined,
      });
    });
    fireEvent.click(screen.getByRole('button', { name: 'Promote' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42/reset-selection', {
        method: 'POST',
        body: undefined,
      });
    });
  });

  it('calls POST restore for archived row', async () => {
    mockDefaultEndpoints([{ ...listItem, archived_at: '2026-01-01T00:00:00Z' }]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByRole('button', { name: 'Restore' });
    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42/restore', {
        method: 'POST',
        body: undefined,
      });
    });
  });

  it('does not crash when action mutation fails', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      const method = init?.method;
      if (path === '/admin/api/provider-endpoints' && !method) {
        return { items: [listItem], page: { limit: 25, offset: 0 } };
      }
      if (path === '/admin/api/provider-settings' && !method) {
        return { items: [] };
      }
      if (path.startsWith('/admin/api/provider-endpoints/')) {
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
