import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import * as client from '../../api/client';
import { GitHubOperatorPanel } from './GitHubOperatorPanel';
vi.mock('../../api/client', () => ({ apiFetch: vi.fn() }));
const settings = { enabled: false, client_id: 'app123', secret_configured: true, allowed_user_ids: ['42'], callback_url: 'https://gateway.test/authorize/github/callback', available: true };
function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><GitHubOperatorPanel /></QueryClientProvider>);
}
afterEach(() => { cleanup(); vi.resetAllMocks(); });
describe('GitHubOperatorPanel', () => {
  it('loads safe settings, preserves a blank secret and saves explicit account IDs', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (_path: string, init?: RequestInit) => init?.method === 'PATCH' ? { ...settings, enabled: true, allowed_user_ids: ['42', '7'] } : settings);
    renderPanel();
    await screen.findByDisplayValue('app123');
    expect(screen.getByText(settings.callback_url)).toBeInTheDocument();
    expect(screen.getByLabelText('GitHub client secret')).toHaveValue('');
    fireEvent.click(screen.getByLabelText('Enable GitHub operator sign-in'));
    fireEvent.change(screen.getByLabelText('Allowed GitHub user IDs'), { target: { value: '42, 7' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save GitHub sign-in' }));
    await waitFor(() => expect(client.apiFetch).toHaveBeenCalledWith('/admin/api/settings/github-operator', {
      method: 'PATCH', body: JSON.stringify({ enabled: true, client_id: 'app123', client_secret: '', allowed_user_ids: ['42', '7'] }),
    }));
    expect(await screen.findByRole('status')).toHaveTextContent('saved');
  });
  it('clears a newly entered secret after successful save', async () => {
    vi.mocked(client.apiFetch).mockResolvedValue(settings);
    renderPanel();
    await screen.findByDisplayValue('app123');
    fireEvent.change(screen.getByLabelText('GitHub client secret'), { target: { value: 'new-test-secret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save GitHub sign-in' }));
    await screen.findByRole('status');
    expect(screen.getByLabelText('GitHub client secret')).toHaveValue('');
  });
  it('shows setup errors without claiming success', async () => {
    vi.mocked(client.apiFetch).mockImplementation(async (_path: string, init?: RequestInit) => { if (init?.method === 'PATCH') throw new Error('Invalid account ID'); return settings; });
    renderPanel();
    await screen.findByDisplayValue('app123');
    fireEvent.click(screen.getByRole('button', { name: 'Save GitHub sign-in' }));
    expect(await screen.findByText('Invalid account ID')).toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});
