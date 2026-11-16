import { useEffect } from 'react';
import { api, errorMessage } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Card } from '../components/Card';
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

export function UpdatesPage() {
  const poll = usePoll((s) => api.update({ signal: s }), 5000);
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
            {u.error && (
              <>
                <br />
                <span className="mono">{u.error}</span>
              </>
            )}
          </Alert>
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
