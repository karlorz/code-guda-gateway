import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { apiFetch, clearCSRFToken, setAuthFailureHandler, setCSRFToken } from './client';
import type { SessionResponse } from './types';

type SessionState = {
  authenticated: boolean;
  loading: boolean;
  login: (token: string) => Promise<void>;
  logout: () => Promise<void>;
  reset: () => void;
};

const SessionContext = createContext<SessionState | undefined>(undefined);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [authenticated, setAuthenticated] = useState(false);
  const [loading, setLoading] = useState(true);

  const reset = useCallback(() => {
    clearCSRFToken();
    setAuthenticated(false);
  }, []);

  useEffect(() => {
    setAuthFailureHandler(reset);
    apiFetch<SessionResponse>('/admin/api/session')
      .then((session) => {
        setCSRFToken(session.csrf_token);
        setAuthenticated(true);
      })
      .catch(() => reset())
      .finally(() => setLoading(false));
    return () => setAuthFailureHandler(undefined);
  }, [reset]);

  const login = useCallback(async (token: string) => {
    const session = await apiFetch<{ status: string; csrf_token: string }>('/admin/api/login', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
    setCSRFToken(session.csrf_token);
    setAuthenticated(true);
  }, []);

  const logout = useCallback(async () => {
    await apiFetch<{ status: string }>('/admin/api/logout', { method: 'POST' }).catch(() => undefined);
    reset();
  }, [reset]);

  const value = useMemo(
    () => ({ authenticated, loading, login, logout, reset }),
    [authenticated, loading, login, logout, reset],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession() {
  const value = useContext(SessionContext);
  if (!value) {
    throw new Error('useSession must be used inside SessionProvider');
  }
  return value;
}
