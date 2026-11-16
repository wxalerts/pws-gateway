import { useEffect } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Card } from '../components/Card';
import { Stat, StatGrid } from '../components/Stat';
import { formatDateTime, formatDuration, formatRelative } from '../lib/format';
import { usePoll } from '../lib/usePoll';

export function UplinkPage() {
  const poll = usePoll((s) => api.uplink({ signal: s }), 5000);
  const health = usePoll((s) => api.health({ signal: s }), 10000);
  const claim = usePoll((s) => api.claim.get({ signal: s }), 10000);

  useEffect(() => {
    document.title = 'Uplink | PWS Gateway';
  }, []);

  const u = poll.data;

  return (
    <>
      <div className="page-header">
        <h1>Uplink</h1>
        {u && (
          <Badge
            tone={
              claim.data && claim.data.state !== 'claimed' ? 'neutral' : u.connected ? 'ok' : 'warn'
            }
          >
            {claim.data && claim.data.state !== 'claimed'
              ? 'Not linked'
              : u.connected
                ? 'Connected'
                : 'Offline'}
          </Badge>
        )}
      </div>
      <div className="stack">
        <p className="muted">
          Every 15 minutes the gateway sends a summary of your selected station to WxAlerts over
          MQTT. If the connection is down, summaries wait in an outbox on the Pi and go out when it
          returns.
        </p>
        {poll.error && !u && <Alert tone="bad">{errorMessage(poll.error)}</Alert>}
        {claim.data && claim.data.state !== 'claimed' && (
          <Alert tone="info" title="Not linked to a WxAlerts account">
            Nothing is uploaded until this gateway is linked. <Link to="/account">Link it now</Link>
          </Alert>
        )}
        {u && claim.data?.state === 'claimed' && !u.connected && (
          <Alert tone="warn" title="Not connected to WxAlerts" live>
            Readings are still being received and stored. Check that the Pi has internet access and
            that your router allows outgoing connections to the broker shown below.
          </Alert>
        )}
        {health.data && !health.data.time.synced && (
          <Alert tone="warn" title="Clock not synced">
            Nothing is uploaded until the Pi trusts its clock, so observations carry the right time.
          </Alert>
        )}
        <div className="grid">
          <Card title="Connection" id="conn">
            {u ? (
              <dl className="kv">
                <dt>Account</dt>
                <dd>
                  {claim.data?.state === 'claimed' ? 'Linked' : 'Not linked'}{' '}
                  <Link to="/account">Manage</Link>
                </dd>
                <dt>Broker</dt>
                <dd className="mono">{u.broker}</dd>
                <dt>Device id</dt>
                <dd className="mono">{u.device_id}</dd>
                <dt>Last upload</dt>
                <dd>
                  {formatRelative(u.last_publish)}
                  {u.last_publish && (
                    <span className="muted"> ({formatDateTime(u.last_publish)})</span>
                  )}
                </dd>
              </dl>
            ) : (
              <p className="muted">Loading...</p>
            )}
          </Card>
          <Card title="Outbox" id="outbox">
            {u ? (
              <StatGrid>
                <Stat
                  label="Waiting to send"
                  value={u.outbox.depth}
                  tone={u.outbox.depth > 20 ? 'warn' : 'neutral'}
                />
                <Stat
                  label="Oldest item"
                  value={u.outbox.depth ? formatDuration(u.outbox.oldest_s) : 'none'}
                />
              </StatGrid>
            ) : (
              <p className="muted">Loading...</p>
            )}
            <p className="muted">
              Summaries wait here while the connection is down and are sent oldest first when it
              returns.
            </p>
          </Card>
        </div>
      </div>
    </>
  );
}
