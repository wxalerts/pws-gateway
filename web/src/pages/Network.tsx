import { useEffect, useState } from 'react';
import { api, errorMessage, type ApplyResult } from '../api';
import { Alert } from '../components/Alert';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { formatDateTime } from '../lib/format';
import { usePoll } from '../lib/usePoll';

/**
 * Placeholder for network settings. The form itself lands later; this page
 * already wires the commit-confirm flow so a bad network change cannot lock
 * anyone out: apply, then confirm from the new address before the deadline, or
 * the gateway reverts on its own.
 */
export function NetworkPage() {
  const config = usePoll((s) => api.config.get({ signal: s }), 10000);
  const [pending, setPending] = useState<ApplyResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'ok' | 'bad'; text: string } | null>(null);

  useEffect(() => {
    document.title = 'Network | PWS Gateway';
  }, []);

  const network = (config.data?.network ?? null) as Record<string, unknown> | null;
  const serverPending = (config.data?.pending_confirm ?? null) as string | null;
  const deadline = pending?.confirm_deadline ?? serverPending;

  async function confirm() {
    setBusy(true);
    setNotice(null);
    try {
      await api.config.confirm();
      setPending(null);
      setNotice({ tone: 'ok', text: 'Settings confirmed and kept.' });
      await config.refresh();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="page-header">
        <h1>Network</h1>
      </div>
      <div className="stack">
        {deadline && (
          <Alert tone="warn" title="Confirm the new settings" live>
            <p>
              A network change was applied. If you can see this page, it worked. Press Confirm
              before <strong>{formatDateTime(deadline)}</strong> to keep it. If you do nothing, the
              gateway goes back to the previous settings so you are never locked out.
            </p>
            <div className="btn-row">
              <Button variant="primary" onClick={() => void confirm()} busy={busy}>
                Confirm and keep
              </Button>
            </div>
          </Alert>
        )}
        {notice && (
          <Alert tone={notice.tone} live>
            {notice.text}
          </Alert>
        )}
        <Card title="How network changes work" id="howto">
          <ol className="steps">
            <li>You change the address, Wi-Fi or hostname and press Apply.</li>
            <li>The gateway switches to the new settings right away and starts a timer.</li>
            <li>You open this page from the new address and press Confirm.</li>
            <li>
              If Confirm does not arrive before the timer runs out, the old settings come back by
              themselves.
            </li>
          </ol>
          <p className="muted">
            This is the same approach network switches use, so a typo in an IP address is never a
            trip to the SD card.
          </p>
        </Card>
        <Card title="Current settings" id="current">
          {config.error && !config.data && <Alert tone="bad">{errorMessage(config.error)}</Alert>}
          {network ? (
            <dl className="kv">
              {Object.entries(network).map(([k, v]) => (
                <KvRow key={k} name={k} value={v} />
              ))}
            </dl>
          ) : (
            <p className="muted">
              {config.data ? 'The gateway did not report network settings.' : 'Loading...'}
            </p>
          )}
          <p className="muted">
            Editing these settings from the browser is coming in a later release.
          </p>
        </Card>
      </div>
    </>
  );
}

function KvRow({ name, value }: { name: string; value: unknown }) {
  const text =
    value === null || value === undefined
      ? 'not set'
      : Array.isArray(value)
        ? value.length
          ? value.join(', ')
          : 'none'
        : typeof value === 'boolean'
          ? value
            ? 'yes'
            : 'no'
          : String(value);
  return (
    <>
      <dt>{name.replace(/_/g, ' ')}</dt>
      <dd className="mono">{text}</dd>
    </>
  );
}
