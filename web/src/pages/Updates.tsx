import { useEffect, useState } from 'react';
import { api, errorMessage, type DeferKind, type UpdateStatus } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { SelectField, TextField } from '../components/fields';
import { Stat, StatGrid } from '../components/Stat';
import { formatDateTime, formatRelative } from '../lib/format';
import { usePoll } from '../lib/usePoll';
import { UPDATE_STATE_LABEL } from './Dashboard';

const EXPLANATION: Record<string, string> = {
  idle: 'No update is in progress. The gateway checks for new releases on its own.',
  downloading: 'A new release is being downloaded and its signature checked.',
  staged:
    'The new release is downloaded and verified. It will be installed at a quiet moment, then the Pi restarts.',
  rebooting: 'The gateway is restarting into the new release. This page reconnects by itself.',
  good: 'The latest release is installed and has passed its health check.',
  rolled_back:
    'The last update did not pass its health check, so the gateway went back to the previous release. Nothing you need to do; the next release will be tried again.',
  failed:
    'The update could not be completed. The current release keeps running. Details are below and in the logs.',
  deferred:
    'An update is ready but was postponed, for example because the uplink was busy or the Pi was on weak power.',
};

const DEFER_TEXT: Record<DeferKind, string> = {
  window: 'Waiting for the update window. The update installs on its own once the window opens.',
  lte: 'This update is a full system image. It waits until the gateway is on Ethernet or Wi-Fi, so it does not use your cellular data.',
  download: 'The download could not finish. The gateway will try again later.',
};

const WINDOW_RE = /^([01]\d|2[0-3]):[0-5]\d-([01]\d|2[0-3]):[0-5]\d$/;

/** Returns an error message for a bad update window, or null when it is fine. */
export function validateWindow(w: string): string | null {
  const v = w.trim();
  if (!WINDOW_RE.test(v)) return 'Use the form 02:00-04:00 (24 hour clock, start then end).';
  const [a, b] = v.split('-');
  if (a === b) return 'The start and end times must be different.';
  return null;
}

const BUSY_STATES = ['downloading', 'staged', 'rebooting'];

export function UpdateSettingsForm({ u, onSaved }: { u: UpdateStatus; onSaved: () => void }) {
  const [win, setWin] = useState(u.window ?? '');
  const [channel, setChannel] = useState<'stable' | 'beta'>(u.channel ?? 'stable');
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: 'ok' | 'bad'; text: string } | null>(null);
  const winError = touched ? validateWindow(win) : null;
  const dirty = win !== (u.window ?? '') || channel !== (u.channel ?? 'stable');

  async function save() {
    setTouched(true);
    if (validateWindow(win)) return;
    setBusy(true);
    setMsg(null);
    try {
      await api.updates.saveSettings({ window: win.trim(), channel });
      setMsg({ tone: 'ok', text: 'Saved.' });
      onSaved();
    } catch (err) {
      setMsg({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="stack form-narrow"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <TextField
        label="Update window"
        value={win}
        onChange={(e) => setWin(e.target.value)}
        onBlur={() => setTouched(true)}
        error={winError}
        hint="Updates install only between these times, in the gateway's local time. Example: 02:00-04:00."
        placeholder="02:00-04:00"
        autoComplete="off"
      />
      <SelectField
        label="Release channel"
        value={channel}
        onChange={setChannel}
        options={[
          { value: 'stable', label: 'Stable (recommended)' },
          { value: 'beta', label: 'Beta (early releases)' },
        ]}
      />
      <div className="btn-row">
        <Button type="submit" variant="primary" busy={busy} disabled={!dirty}>
          Save
        </Button>
      </div>
      {msg && (
        <Alert tone={msg.tone} live>
          {msg.text}
        </Alert>
      )}
    </form>
  );
}

export function UpdatesPage() {
  const poll = usePoll((s) => api.update({ signal: s }), 5000);
  const [busy, setBusy] = useState<'check' | 'install' | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'ok' | 'bad'; text: string } | null>(null);

  async function run(kind: 'check' | 'install') {
    setBusy(kind);
    setNotice(null);
    setConfirming(false);
    try {
      if (kind === 'check') await api.updates.check();
      else await api.updates.install();
      setNotice({
        tone: 'ok',
        text:
          kind === 'check'
            ? 'Checking for a new release.'
            : 'Installing now. The gateway will restart when the download is verified.',
      });
      await poll.refresh();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusy(null);
    }
  }
  const version = usePoll((s) => api.version({ signal: s }), 60000);

  useEffect(() => {
    document.title = 'Updates | PWS Gateway';
  }, []);

  const u = poll.data;
  const meta = u
    ? (UPDATE_STATE_LABEL[u.state] ?? { label: u.state, tone: 'neutral' as const })
    : null;

  return (
    <>
      <div className="page-header">
        <h1>Updates</h1>
        {u && meta && <Badge tone={meta.tone}>{meta.label}</Badge>}
      </div>
      <div className="stack">
        <p className="muted">
          Updates are signed releases that the gateway downloads, verifies and installs by itself.
          If a new release fails its health check after restarting, the Pi returns to the previous
          one automatically.
        </p>
        {poll.error && !u && <Alert tone="bad">{errorMessage(poll.error)}</Alert>}
        {u && (
          <Alert
            tone={meta?.tone === 'bad' ? 'bad' : meta?.tone === 'warn' ? 'warn' : 'info'}
            title={meta?.label}
            live
          >
            {EXPLANATION[u.state] ?? u.state}
            {u.state === 'deferred' && u.defer_kind && <p>{DEFER_TEXT[u.defer_kind]}</p>}
            {u.detail && <p>{u.detail}</p>}
            {(u.state === 'failed' || u.state === 'rolled_back') && u.error && (
              <p>
                {u.state === 'failed' && u.code && (
                  <>
                    <Badge tone="bad">{u.code}</Badge>{' '}
                  </>
                )}
                <span className="mono">{u.error}</span>
              </p>
            )}
            {u.state === 'rebooting' && (
              <p>
                Reconnecting: this page keeps trying and comes back by itself in about a minute. If
                it does not, reload the page.
              </p>
            )}
          </Alert>
        )}
        {u && u.enabled === false && (
          <Alert tone="info">This is a development build, updates are off.</Alert>
        )}
        {u?.state === 'downloading' && (
          <div>
            <label htmlFor="update-progress">Download progress</label>{' '}
            <progress id="update-progress" max={100} value={u.progress ?? 0}>
              {u.progress ?? 0}%
            </progress>{' '}
            <span>{u.progress ?? 0}%</span>
          </div>
        )}
        {notice && (
          <Alert tone={notice.tone} live>
            {notice.text}
          </Alert>
        )}
        {u && (
          <Card title="Update now" id="actions">
            {u.os_deferred && (
              <p>
                A full system image is waiting. It is held back until the gateway is on Ethernet or
                Wi-Fi.
              </p>
            )}
            <div className="btn-row">
              <Button
                onClick={() => void run('check')}
                busy={busy === 'check'}
                disabled={BUSY_STATES.includes(u.state) || u.enabled === false}
              >
                Check now
              </Button>
              <Button
                variant="primary"
                onClick={() => setConfirming(true)}
                disabled={BUSY_STATES.includes(u.state) || busy !== null || u.enabled === false}
              >
                Install now
              </Button>
            </div>
            {confirming && (
              <Alert tone="warn" title="Install now?" live>
                <p>
                  This skips the update window. The Pi restarts during the install, so the weather
                  station is offline for a minute or two.
                </p>
                <div className="btn-row">
                  <Button variant="danger" onClick={() => void run('install')}>
                    Yes, install and restart
                  </Button>
                  <Button variant="ghost" onClick={() => setConfirming(false)}>
                    Cancel
                  </Button>
                </div>
              </Alert>
            )}
          </Card>
        )}
        {u && u.window !== undefined && (
          <Card title="Update window and channel" id="settings">
            <UpdateSettingsForm u={u} onSaved={() => void poll.refresh()} />
          </Card>
        )}
        <div className="grid">
          <Card title="Status" id="status">
            {u ? (
              <StatGrid>
                <Stat label="Installed release" value={u.current} />
                <Stat label="Target release" value={u.target ?? 'none'} />
                <Stat
                  label="Last change"
                  value={formatRelative(u.ts)}
                  hint={formatDateTime(u.ts)}
                />
                <Stat
                  label="Last check"
                  value={u.last_check ? formatRelative(u.last_check) : 'not yet'}
                  hint={u.last_check ? formatDateTime(u.last_check) : undefined}
                />
              </StatGrid>
            ) : (
              <p className="muted">Loading...</p>
            )}
          </Card>
          <Card title="Running build" id="build">
            {version.data ? (
              <dl className="kv">
                <dt>Version</dt>
                <dd className="mono">{version.data.version}</dd>
                <dt>Commit</dt>
                <dd className="mono">{version.data.commit}</dd>
                <dt>Built</dt>
                <dd>{formatDateTime(version.data.build_date)}</dd>
              </dl>
            ) : (
              <p className="muted">{version.error ? errorMessage(version.error) : 'Loading...'}</p>
            )}
          </Card>
        </div>
      </div>
    </>
  );
}
