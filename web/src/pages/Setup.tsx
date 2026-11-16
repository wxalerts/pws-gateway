import { useEffect, useState, type FormEvent } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import { errorMessage } from '../api';
import { useAuth } from '../auth/AuthContext';
import { Alert } from '../components/Alert';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { TextField } from '../components/fields';

export const MIN_PASSWORD_LENGTH = 12;

export function SetupPage() {
  const { status, completeSetup } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    document.title = 'Set up | PWS Gateway';
  }, []);

  if (status === 'signed_in') return <Navigate to="/" replace />;
  if (status === 'signed_out') return <Navigate to="/login" replace />;

  // Must match auth.MinPasswordLength on the server, which rejects anything
  // shorter with weak_password. Counted in code points there, so use the
  // spread length here rather than UTF-16 units.
  const passwordLength = [...password].length;
  const tooShort = passwordLength > 0 && passwordLength < MIN_PASSWORD_LENGTH;
  const mismatch = confirm.length > 0 && confirm !== password;
  const canSubmit =
    username.trim().length > 0 &&
    passwordLength >= MIN_PASSWORD_LENGTH &&
    confirm === password &&
    !busy;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setError(null);
    try {
      await completeSetup({ username: username.trim(), password });
      navigate('/', { replace: true });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-page">
      <Card title="Welcome to your weather gateway" className="auth-card" id="setup">
        <p>
          Choose a username and password for this gateway. You will use them to sign in from any
          device on your home network.
        </p>
        <form onSubmit={onSubmit} noValidate>
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
            autoComplete="new-password"
            hint={`At least ${MIN_PASSWORD_LENGTH} characters. Pick something you do not use anywhere else.`}
            error={tooShort ? `Needs at least ${MIN_PASSWORD_LENGTH} characters.` : undefined}
            required
          />
          <TextField
            label="Repeat password"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
            error={mismatch ? 'The two passwords do not match.' : undefined}
            required
          />
          {error && (
            <Alert tone="bad" live>
              {error}
            </Alert>
          )}
          <div className="btn-row">
            <Button type="submit" variant="primary" busy={busy} disabled={!canSubmit}>
              Create account
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
