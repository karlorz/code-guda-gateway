import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LoginPage } from './LoginPage';

const login = vi.fn();

vi.mock('../../api/session', () => ({
  useSession: () => ({
    authenticated: false,
    loading: false,
    login,
    logout: vi.fn(),
    reset: vi.fn(),
  }),
}));

describe('LoginPage', () => {
  beforeEach(() => {
    login.mockReset();
    login.mockResolvedValue(undefined);
  });

  afterEach(() => {
    cleanup();
  });

  it('exposes a username/password form browsers can treat as a login pair', () => {
    render(<LoginPage />);
    const form = document.querySelector('form');
    expect(form).toHaveAttribute('method', 'post');
    expect(form).toHaveAttribute('action', '/admin/api/login');
    expect(form).toHaveAttribute('autoComplete', 'on');

    const username = screen.getByLabelText(/^username$/i);
    expect(username).toHaveAttribute('name', 'username');
    expect(username).toHaveAttribute('autoComplete', 'username');
    expect(username).toHaveAttribute('type', 'text');
    expect(username).toHaveValue('admin');

    const password = screen.getByLabelText(/^password$/i);
    expect(password).toHaveAttribute('name', 'password');
    expect(password).toHaveAttribute('autoComplete', 'current-password');
    expect(password).toHaveAttribute('type', 'password');
  });

  it('submits only the password field as the admin token', async () => {
    render(<LoginPage />);
    fireEvent.change(screen.getByLabelText(/^username$/i), { target: { value: 'ops' } });
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'secret-token' } });
    fireEvent.click(screen.getByRole('button', { name: /log in/i }));
    await waitFor(() => expect(login).toHaveBeenCalledWith('secret-token'));
  });

  it('shows a clear invalid-token message instead of a raw status code', async () => {
    login.mockRejectedValue(new Error('Invalid admin token. Check the password and try again.'));
    render(<LoginPage />);
    fireEvent.change(screen.getByLabelText(/^password$/i), { target: { value: 'wrong' } });
    fireEvent.click(screen.getByRole('button', { name: /log in/i }));
    expect(await screen.findByText(/invalid admin token/i)).toBeInTheDocument();
    expect(screen.queryByText(/request failed: 401/i)).not.toBeInTheDocument();
  });
});
