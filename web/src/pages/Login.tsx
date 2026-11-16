import { useEffect, useState, type FormEvent } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { errorMessage } from '../api';
import { useAuth } from '../auth/AuthContext';
import { Alert } from '../components/Alert';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { TextField } from '../components/fields';

export function LoginPage() {
  const { status, login } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? '/';
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    document.title = 'Sign in | PWS Gateway';
  }, []);

  if (status === 'signed_in') return <Navigate to={from} replace />;
  if (status === 'needs_setup') return <Navigate to="/setup" replace />;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login({ username: username.trim(), password });
      navigate(from, { replace: true });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-page">
      <Card title="Sign in to PWS Gateway" className="auth-card" id="login">
        {status === 'checking' && (
          <p className="muted" role="status">
            Checking for an existing session...
          </p>
        )}
        <form onSubmit={onSubmit}>
          <TextField
            label="Username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
          />
          <TextField
            label="Password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
          {error && (
            <Alert tone="bad" live>
              {error}
            </Alert>
          )}
          <div className="btn-row">
            <Button type="submit" variant="primary" busy={busy}>
              Sign in
            </Button>
          </div>
        </form>
        <p className="muted">
          Forgot the password? The recovery steps are in the gateway documentation. Your stations
          and readings are kept.
        </p>
      </Card>
    </div>
  );
}
