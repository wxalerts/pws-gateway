import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, type Claim } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { QrCode } from '../components/QrCode';
import { CLAIM_PAGE, spokenCode, WXALERTS_DASHBOARD } from '../lib/claim';
import { formatClock, useCountdown } from '../lib/useCountdown';
import { usePoll } from '../lib/usePoll';

const END_REASON: Record<NonNullable<Claim['error']>, string> = {
  expired: 'The code ran out of time before it was entered. Get a new one to try again.',
  denied: 'The code was turned down on the WxAlerts site. Get a new code to try again.',
  failed: 'Linking did not finish. Check the logs page, then get a new code.',
};

export function PendingClaim({
  claim,
  busy,
  onStartOver,
  onCancel,
  onExpired,
}: {
  claim: Claim;
  busy: boolean;
  onStartOver: () => void;
  onCancel: () => void;
  onExpired: () => void;
}) {
  const left = useCountdown(claim.expires_at);
  useEffect(() => {
    if (left === 0) onExpired();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [left === 0]);
  const uri = claim.verification_uri ?? CLAIM_PAGE;
  const qrTarget = claim.verification_uri_complete ?? uri;
  const code = claim.user_code ?? '';
  return (
    <Card title="Link this gateway" id="claim">
      <div className="claim-layout">
        <div className="stack">
          <ol className="claim-steps">
            <li>
              On a phone or computer, open{' '}
              <a href={uri} target="_blank" rel="noopener noreferrer">
                {uri}
              </a>{' '}
              and sign in to WxAlerts.
            </li>
            <li>Enter this code:</li>
          </ol>
          <p className="claim-code" aria-label={`Code: ${spokenCode(code)}`}>
            <span aria-hidden="true">{code}</span>
          </p>
          <p
            role="timer"
            aria-live="off"
            className={left <= 60 ? 'claim-timer warn' : 'claim-timer'}
          >
            {left > 0 ? `Code expires in ${formatClock(left)}` : 'Code expired'}
          </p>
          <p className="muted">
            This page updates by itself once the code is accepted. Nothing needs to be done on the
            Pi.
          </p>
          <div className="claim-actions">
            <Button variant="secondary" onClick={onStartOver} busy={busy}>
              Start over
            </Button>
            <Button variant="ghost" onClick={onCancel} busy={busy}>
              Cancel
            </Button>
          </div>
        </div>
        <div className="claim-qr">
          <QrCode value={qrTarget} label="QR code for the WxAlerts link page" />
          <p className="muted">
            Or scan this with a phone. It opens the page with the code filled in.
          </p>
        </div>
      </div>
    </Card>
  );
}

export function AccountPage() {
  const claim = usePoll((s) => api.claim.get({ signal: s }), 3000);
  const uplink = usePoll((s) => api.uplink({ signal: s }), 5000);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [confirmUnlink, setConfirmUnlink] = useState(false);

  useEffect(() => {
    document.title = 'WxAlerts account | PWS Gateway';
  }, []);

  const c = claim.data;

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setProblem(null);
    try {
      await fn();
    } catch (err) {
      setProblem(errorMessage(err));
    } finally {
      setBusy(false);
      await claim.refresh();
    }
  }

  return (
    <>
      <div className="page-header">
        <h1>WxAlerts account</h1>
        {c && (
          <Badge tone={c.state === 'claimed' ? 'ok' : c.state === 'pending' ? 'info' : 'neutral'}>
            {c.state === 'claimed'
              ? 'Linked'
              : c.state === 'pending'
                ? 'Waiting for code'
                : 'Not linked'}
          </Badge>
        )}
      </div>
      <div className="stack">
        {claim.error && !c && <Alert tone="bad">{errorMessage(claim.error)}</Alert>}
        {problem && (
          <Alert tone="bad" title="That did not work" live>
            {problem}
          </Alert>
        )}

        {c?.state === 'unclaimed' && (
          <Card title="Link this gateway to WxAlerts" id="claim-start">
            <div className="stack">
              {c.error && (
                <Alert tone="warn" live>
                  {END_REASON[c.error]}
                </Alert>
              )}
              <p>
                Linking sends your selected station&apos;s summaries to your WxAlerts account. You
                get a short code here, enter it on the WxAlerts site, and the gateway finishes the
                rest by itself. The gateway never sees your WxAlerts password.
              </p>
              <p>
                <Button
                  variant="primary"
                  busy={busy}
                  onClick={() => void run(() => api.claim.start())}
                >
                  Get a code
                </Button>
              </p>
            </div>
          </Card>
        )}

        {c?.state === 'pending' && (
          <PendingClaim
            claim={c}
            busy={busy}
            onStartOver={() => void run(() => api.claim.start())}
            onCancel={() => void run(() => api.claim.cancel())}
            onExpired={() => void claim.refresh()}
          />
        )}

        {c?.state === 'claimed' && (
          <>
            <Card title="This gateway is linked" id="claim-done">
              <div className="stack">
                <dl className="kv">
                  <dt>Device id</dt>
                  <dd className="mono">{c.device_id}</dd>
                  <dt>Connection</dt>
                  <dd>
                    {uplink.data ? (
                      <Badge tone={uplink.data.connected ? 'ok' : 'warn'}>
                        {uplink.data.connected ? 'Connected' : 'Offline'}
                      </Badge>
                    ) : (
                      <span className="muted">Checking...</span>
                    )}
                  </dd>
                </dl>
                <p>
                  <a href={WXALERTS_DASHBOARD} target="_blank" rel="noopener noreferrer">
                    Open your WxAlerts dashboard
                  </a>{' '}
                  to see this gateway and its station. <Link to="/uplink">Uplink details</Link>
                </p>
              </div>
            </Card>
            <Card title="Unlink" id="claim-unlink">
              <div className="stack">
                <p>
                  Unlinking stops uploads and deletes the sign-in details stored on this Pi. The
                  gateway keeps collecting readings locally. To also remove it from your account, do
                  that on the WxAlerts site; until then a new code cannot be issued.
                </p>
                {confirmUnlink ? (
                  <div className="claim-actions">
                    <Button
                      variant="danger"
                      busy={busy}
                      onClick={() =>
                        void run(async () => {
                          await api.claim.unlink();
                          setConfirmUnlink(false);
                        })
                      }
                    >
                      Yes, unlink this gateway
                    </Button>
                    <Button variant="ghost" onClick={() => setConfirmUnlink(false)}>
                      Keep it linked
                    </Button>
                  </div>
                ) : (
                  <p>
                    <Button variant="secondary" onClick={() => setConfirmUnlink(true)}>
                      Unlink this gateway
                    </Button>
                  </p>
                )}
              </div>
            </Card>
          </>
        )}
        {!c && claim.loading && <p className="muted">Loading...</p>}
      </div>
    </>
  );
}
