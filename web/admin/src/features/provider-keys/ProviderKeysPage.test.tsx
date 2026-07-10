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
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
}

const listItem = {
  ID: 42,
  Provider: 'grok',
  Name: 'primary',
  KeyPrefix: 'xai-',
  Enabled: true,
  BaseURL: 'https://api.x.ai/v1',
  QuotaMode: 'disabled',
  QuotaFlow: 'grok2api_admin',
  QuotaKeyConfigured: false,
};

const listItemAlt = {
  ID: 43,
  Provider: 'tavily',
  Name: 'tv-proxy',
  KeyPrefix: 'tvly-',
  Enabled: true,
  BaseURL: 'https://proxy.example/tavily',
  QuotaMode: 'endpoint_credentials',
  QuotaFlow: 'tavily_usage',
  QuotaKeyConfigured: false,
};

const listItemSeparate = {
  ID: 44,
  Provider: 'grok',
  Name: 'new-api-sg',
  KeyPrefix: 'xai-',
  Enabled: true,
  BaseURL: 'https://new-api.example/v1',
  QuotaMode: 'separate_credentials',
  QuotaFlow: 'grok2api_admin',
  QuotaBaseURL: 'https://grok2api.example',
  QuotaKeyConfigured: true,
  QuotaKeyPrefix: 'g2a-',
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
    if (path.startsWith('/admin/api/provider-settings/') && method === 'PATCH') {
      return { status: 'ok' };
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

  it('opens endpoint create in a side sheet', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: /add endpoint/i }));
    const dialog = await screen.findByRole('dialog', { name: /create endpoint/i });
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByText(/identity/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/inference route/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/quota source/i)).toBeInTheDocument();
  });

  it('explains that endpoint name does not define routing priority', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    expect(screen.getByText(/does not define routing priority/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /add endpoint/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/does not define routing priority/i)).toBeInTheDocument();
  });

  it('submits nested quota configuration to provider-endpoints', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: /add endpoint/i }));
    const dialog = await screen.findByRole('dialog', { name: /create endpoint/i });

    fireEvent.change(within(dialog).getByLabelText(/^name$/i), { target: { value: 'new-ep' } });
    fireEvent.change(within(dialog).getByLabelText(/^base url$/i), { target: { value: 'https://custom.example/v1' } });
    const keyInput = within(dialog).getByLabelText(/^key$/i);
    expect(keyInput).toHaveAttribute('type', 'password');
    fireEvent.change(keyInput, { target: { value: 'xai-secret-create-key-abcdef' } });
    fireEvent.change(within(dialog).getByLabelText(/quota mode/i), { target: { value: 'separate_credentials' } });
    fireEvent.change(within(dialog).getByLabelText(/quota base url/i), {
      target: { value: 'https://grok2api.example' },
    });
    fireEvent.change(within(dialog).getByLabelText(/^quota key$/i), {
      target: { value: 'g2a-admin-quota-secret-key-2222' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: /create endpoint/i }));

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
            quota: {
              mode: 'separate_credentials',
              flow: 'grok2api_admin',
              base_url: 'https://grok2api.example',
              key: 'g2a-admin-quota-secret-key-2222',
            },
          }),
        }),
      );
    });
  });

  it('summarizes inference and quota configuration without primary or backup roles', async () => {
    mockDefaultEndpoints([listItem, listItemAlt, listItemSeparate]);
    renderWithClient(<ProviderKeysPage />);
    expect(await screen.findByText('https://api.x.ai/v1')).toBeInTheDocument();
    expect(screen.getByText('https://proxy.example/tavily')).toBeInTheDocument();
    expect(screen.getAllByText(/inference/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/disabled/i)).toBeInTheDocument();
    expect(screen.getByText(/use inference url and key/i)).toBeInTheDocument();
    expect(screen.getByText(/separate credentials/i)).toBeInTheDocument();
    expect(screen.queryByText(/primary role/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/backup role/i)).not.toBeInTheDocument();
  });

  it('shows creation defaults collapsed below the add action', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    const defaultsToggle = screen.getByRole('button', { name: /new endpoint defaults/i });
    expect(defaultsToggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByLabelText(/default base url for new endpoints/i)).not.toBeInTheDocument();
    fireEvent.click(defaultsToggle);
    expect(defaultsToggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getAllByLabelText(/default base url for new endpoints/i).length).toBeGreaterThan(0);
  });

  it('states defaults affect future endpoints only', async () => {
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: /new endpoint defaults/i }));
    expect(screen.getByText(/defaults affect future endpoints only/i)).toBeInTheDocument();
  });

  it('links to Provider Monitoring', async () => {
    renderWithClient(<ProviderKeysPage />);
    const link = await screen.findByRole('link', { name: /provider monitoring/i });
    expect(link).toHaveAttribute('href', '/providers');
  });

  it('table shows mixed row-owned base URLs', async () => {
    renderWithClient(<ProviderKeysPage />);
    expect(await screen.findByText('https://api.x.ai/v1')).toBeInTheDocument();
    expect(screen.getByText('https://proxy.example/tavily')).toBeInTheDocument();
  });

  it('keeps enable and archive controls', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
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

  it('calls POST archive on canonical endpoint path', async () => {
    mockDefaultEndpoints([listItem]);
    renderWithClient(<ProviderKeysPage />);
    await screen.findByText('primary');
    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith('/admin/api/provider-endpoints/42/archive', {
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
