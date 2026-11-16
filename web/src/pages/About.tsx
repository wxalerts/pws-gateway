import { useEffect } from 'react';
import { api, errorMessage } from '../api';
import { Alert } from '../components/Alert';
import { Card } from '../components/Card';
import { formatDateTime } from '../lib/format';
import { usePoll } from '../lib/usePoll';

export function AboutPage() {
  const version = usePoll((s) => api.version({ signal: s }), 300_000);
  const health = usePoll((s) => api.health({ signal: s }), 60_000);

  useEffect(() => {
    document.title = 'About | PWS Gateway';
  }, []);

  return (
    <>
      <div className="page-header">
        <h1>About</h1>
      </div>
      <div className="grid">
        <Card title="Software" id="software">
          {version.error && !version.data && (
            <Alert tone="bad">{errorMessage(version.error)}</Alert>
          )}
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
            <p className="muted">Loading...</p>
          )}
          <p className="muted">
            WxAlerts PWS Gateway is open source under the MIT license. It runs rtl_433 as a separate
            program, which keeps its own license.
          </p>
        </Card>
        <Card title="Hardware" id="hardware">
          {health.data ? (
            <dl className="kv">
              <dt>Board</dt>
              <dd>{health.data.board.model}</dd>
              <dt>Chip</dt>
              <dd className="mono">{health.data.board.soc}</dd>
              <dt>Architecture</dt>
              <dd className="mono">{health.data.board.arch}</dd>
              <dt>Support tier</dt>
              <dd>{health.data.board.tier}</dd>
              <dt>Firmware</dt>
              <dd className="mono">{health.data.fw}</dd>
            </dl>
          ) : (
            <p className="muted">{health.error ? errorMessage(health.error) : 'Loading...'}</p>
          )}
        </Card>
        <Card title="Help" id="help">
          <p>
            Documentation, the hardware list and the issue tracker live in the project repository.
            When reporting a problem, include the version above and a download from the Logs page.
          </p>
          <p>
            <a href="https://github.com/wxalerts/pws-gateway" rel="noreferrer">
              github.com/wxalerts/pws-gateway
            </a>
          </p>
        </Card>
      </div>
    </>
  );
}
