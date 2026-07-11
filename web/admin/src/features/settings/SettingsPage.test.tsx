import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as client from '../../api/client';
import { SettingsPage } from './SettingsPage';

vi.mock('../../api/client', () => ({ apiFetch: vi.fn() }));

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('SettingsPage', () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string) => {
      if (path === '/admin/api/settings/display-timezone') {
        return { timezone: 'Asia/Seoul', source: 'host' };
      }
      return {};
    });
  });

  it('shows runtime guidance and display timezone panel', async () => {
    renderPage();
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getByText('/admin')).toBeInTheDocument();
    expect(screen.getByText(/defaults apply only to newly created endpoints/i)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByDisplayValue('Asia/Seoul')).toBeInTheDocument());
    expect(screen.getByText('host default')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^save$/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /use host timezone/i })).toBeInTheDocument();
  });

  it('saves a timezone via PATCH', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === '/admin/api/settings/display-timezone' && init?.method === 'PATCH') {
        return { timezone: 'UTC', source: 'stored' };
      }
      if (path === '/admin/api/settings/display-timezone') {
        return { timezone: 'Asia/Seoul', source: 'host' };
      }
      return {};
    });
    renderPage();
    await waitFor(() => expect(screen.getByDisplayValue('Asia/Seoul')).toBeInTheDocument());
    const input = screen.getByLabelText(/timezone \(iana\)/i);
    fireEvent.change(input, { target: { value: 'UTC' } });
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => {
      expect(vi.mocked(client.apiFetch)).toHaveBeenCalledWith(
        '/admin/api/settings/display-timezone',
        expect.objectContaining({ method: 'PATCH' }),
      );
    });
  });
});
