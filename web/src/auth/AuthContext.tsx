import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { api, isApiError, onUnauthenticated, type Credentials } from '../api';

export type AuthStatus = 'checking' | 'needs_setup' | 'signed_out' | 'signed_in';

interface AuthContextValue {
  status: AuthStatus;
  username: string | null;
  login: (creds: Credentials) => Promise<void>;
  logout: () => Promise<void>;
  completeSetup: (creds: Credentials) => Promise<void>;
  /** Re-probe /api/setup and /api/session. */
  recheck: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('checking');
  const [username, setUsername] = useState<string | null>(null);

  const recheck = useCallback(async () => {
    try {
      const setup = await api.setup.status();
      if (setup.needs_setup) {
        setStatus('needs_setup');
        setUsername(null);
        return;
      }
      const session = await api.session.get();
      if (session.authenticated) {
        setUsername(session.username);
        setStatus('signed_in');
      } else {
        setStatus('signed_out');
      }
    } catch (err) {
      if (isApiError(err) && err.status === 401) {
        setStatus('signed_out');
        setUsername(null);
        return;
      }
      // Network failure or server error: treat as signed out so the person
      // sees the login page with its error rather than a blank screen.
      setStatus('signed_out');
    }
  }, []);

  useEffect(() => {
    void recheck();
  }, [recheck]);

  // Any 401 from any request drops us to the login page.
  useEffect(() => {
    onUnauthenticated(() => {
      setStatus('signed_out');
      setUsername(null);
    });
    return () => onUnauthenticated(null);
  }, []);

  const login = useCallback(async (creds: Credentials) => {
    const session = await api.session.login(creds);
    setUsername(session.username);
    setStatus('signed_in');
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.session.logout();
    } finally {
      setUsername(null);
      setStatus('signed_out');
    }
  }, []);

  const completeSetup = useCallback(
    async (creds: Credentials) => {
      await api.setup.create(creds);
      await login(creds);
    },
    [login],
  );

  const value = useMemo(
    () => ({ status, username, login, logout, completeSetup, recheck }),
    [status, username, login, logout, completeSetup, recheck],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}

/** Wrap routes that need a session. Sends people to setup or login as needed. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { status } = useAuth();
  const location = useLocation();
  if (status === 'checking') {
    return (
      <p className="screen-center muted" role="status" aria-live="polite">
        Connecting to the gateway...
      </p>
    );
  }
  if (status === 'needs_setup') return <Navigate to="/setup" replace />;
  if (status === 'signed_out') {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return <>{children}</>;
}
