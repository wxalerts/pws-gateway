import { useEffect, useRef, useState } from 'react';
import {
  api,
  errorMessage,
  type NetConnection,
  type NetworkStatus,
  type WifiNetwork,
} from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { RadioGroup, TextField } from '../components/fields';
import { Table } from '../components/Table';
import { formatClock, useCountdown } from '../lib/useCountdown';
import { usePoll } from '../lib/usePoll';

type Notice = { tone: 'ok' | 'bad' | 'warn'; text: string };
type Pending = { deadline: string; changed: string[] };

const CONNECTIVITY: Record<
  NetworkStatus['connectivity'],
  { label: string; tone: 'ok' | 'warn' | 'bad' | 'neutral' }
> = {
  full: { label: 'Online', tone: 'ok' },
  limited: { label: 'Connected, no internet', tone: 'warn' },
  none: { label: 'Not connected', tone: 'bad' },
  unknown: { label: 'Unknown', tone: 'neutral' },
};

/** Split a list typed with commas, spaces or new lines. */
function splitList(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

interface EthDraft {
  method: 'auto' | 'manual';
  address: string;
  gateway: string;
  dns: string;
  search: string;
}

function ethDraftOf(c: NetConnection | undefined): EthDraft {
  return {
    method: c?.ipv4.method === 'manual' ? 'manual' : 'auto',
    address: c?.ipv4.address ?? '',
    gateway: c?.ipv4.gateway ?? '',
    dns: (c?.ipv4.dns ?? []).join(', '),
    search: (c?.ipv4.dns_search ?? []).join(', '),
  };
}

function sameEth(a: EthDraft, b: EthDraft): boolean {
  if (a.method !== b.method) return false;
  if (a.method === 'auto') {
    return (
      splitList(a.dns).join() === splitList(b.dns).join() &&
      splitList(a.search).join() === splitList(b.search).join()
    );
  }
  return (
    a.address.trim() === b.address.trim() &&
    a.gateway.trim() === b.gateway.trim() &&
    splitList(a.dns).join() === splitList(b.dns).join() &&
    splitList(a.search).join() === splitList(b.search).join()
  );
}

function stripPrefix(cidr: string): string {
  return cidr.trim().split('/')[0];
}

/** The banner shown after Apply until the change is confirmed or reverted. */
export function ConfirmBanner({
  pending,
  onConfirm,
  onUndo,
  onExpired,
  busy,
}: {
  pending: Pending;
  onConfirm: () => void;
  onUndo: () => void;
  onExpired: () => void;
  busy: boolean;
}) {
  const secs = useCountdown(pending.deadline);
  const expired = useRef(onExpired);
  expired.current = onExpired;
  useEffect(() => {
    if (secs === 0) expired.current();
  }, [secs]);
  return (
    <Alert tone="warn" title="Confirm the new network settings" live>
      <p>
        If you can see this page from the new address, the change worked. Press{' '}
        <strong>Confirm and keep</strong> to make it permanent. If you do nothing, the gateway goes
        back to the previous settings by itself in 90 seconds.
      </p>
      <p>
        Time left: <strong>{formatClock(secs)}</strong>
        {pending.changed.length > 0 && <> (changed: {pending.changed.join(', ')})</>}
      </p>
      <div className="btn-row">
        <Button variant="primary" onClick={onConfirm} busy={busy}>
          Confirm and keep
        </Button>
        <Button variant="danger" onClick={onUndo} disabled={busy}>
          Undo now
        </Button>
      </div>
    </Alert>
  );
}

export function NetworkPage() {
  const status = usePoll((s) => api.network.get({ signal: s }), 5000);
  const [local, setLocal] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [applyError, setApplyError] = useState<string | null>(null);

  const [eth, setEth] = useState<EthDraft | null>(null);
  const [hostname, setHostname] = useState('');
  const [ssid, setSsid] = useState('');
  const [psk, setPsk] = useState('');
  const [hidden, setHidden] = useState(false);
  const [scan, setScan] = useState<WifiNetwork[] | null>(null);
  const [scanning, setScanning] = useState(false);
  const [scanError, setScanError] = useState<string | null>(null);
  const [warnAsk, setWarnAsk] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  useEffect(() => {
    document.title = 'Network | PWS Gateway';
  }, []);

  const data = status.data;
  const ethConn = data?.connections.find((c) => c.type === 'ethernet');
  const original = ethDraftOf(ethConn);

  // Fill the form once the first status arrives, and again after a change lands.
  const loaded = useRef(false);
  useEffect(() => {
    if (data && !loaded.current) {
      loaded.current = true;
      setEth(ethDraftOf(data.connections.find((c) => c.type === 'ethernet')));
      setHostname(data.hostname);
    }
  }, [data]);

  function resetForm(next: NetworkStatus | null) {
    if (!next) return;
    setEth(ethDraftOf(next.connections.find((c) => c.type === 'ethernet')));
    setHostname(next.hostname);
    setSsid('');
    setPsk('');
    setHidden(false);
    setWarnAsk(false);
  }

  async function refreshAndReset() {
    const fresh = await api.network.get();
    resetForm(fresh);
    await status.refresh();
  }

  const pending: Pending | null = data?.pending ?? local;

  const ethChanged = !!eth && !!ethConn && !sameEth(eth, original);
  const hostChanged = !!data && hostname.trim() !== data.hostname;
  const wifiNew = ssid.trim() !== '';
  const changedCount = (ethChanged ? 1 : 0) + (wifiNew ? 1 : 0) + (hostChanged ? 1 : 0);

  const browsing = window.location.hostname;
  let addressWarning: string | null = null;
  if (ethChanged && eth) {
    if (eth.method === 'manual' && stripPrefix(eth.address) !== browsing) {
      addressWarning = `You are opening this page at ${browsing}. After Apply the Ethernet address will be ${stripPrefix(eth.address) || 'different'}, so this page will stop answering at the old address. Open the new address in your browser, then press Confirm.`;
    } else if (eth.method === 'auto' && original.method === 'manual') {
      addressWarning = `You are opening this page at ${browsing}. Switching to automatic (DHCP) gives the gateway an address from your router, which may be different. Find the new address in your router, open it, then press Confirm.`;
    }
  }
  if (!addressWarning && hostChanged && browsing === data?.hostname) {
    addressWarning = `You are opening this page as ${browsing}. After the name changes, use the new name or the IP address.`;
  }

  async function doApply() {
    if (!data || !eth) return;
    setBusy(true);
    setApplyError(null);
    setNotice(null);
    setWarnAsk(false);
    const connections: NetConnection[] = [];
    if (ethChanged && ethConn) {
      connections.push({
        ...ethConn,
        ipv4:
          eth.method === 'manual'
            ? {
                method: 'manual',
                address: eth.address.trim(),
                gateway: eth.gateway.trim() || undefined,
                dns: splitList(eth.dns),
                dns_search: splitList(eth.search),
              }
            : {
                method: 'auto',
                dns: splitList(eth.dns),
                dns_search: splitList(eth.search),
              },
      });
    }
    if (wifiNew) {
      const name = ssid.trim();
      connections.push({
        name,
        type: 'wifi',
        autoconnect: true,
        ipv4: { method: 'auto' },
        wifi: { ssid: name, hidden: hidden || undefined, psk: psk || undefined },
      });
    }
    try {
      const res = await api.network.apply({
        connections,
        ...(hostChanged ? { hostname: hostname.trim() } : {}),
      });
      setLocal({ deadline: res.confirm_deadline, changed: res.changed });
      setPsk('');
      await status.refresh();
    } catch (err) {
      setApplyError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function onApplyClick() {
    if (addressWarning) setWarnAsk(true);
    else void doApply();
  }

  async function confirm() {
    setBusy(true);
    setNotice(null);
    try {
      await api.network.confirm();
      setLocal(null);
      setNotice({ tone: 'ok', text: 'Network settings confirmed and kept.' });
      await refreshAndReset();
    } catch (err) {
      setLocal(null);
      setNotice({ tone: 'bad', text: errorMessage(err) });
      await status.refresh();
    } finally {
      setBusy(false);
    }
  }

  async function undo() {
    setBusy(true);
    setNotice(null);
    try {
      await api.network.revert();
      setLocal(null);
      setNotice({ tone: 'ok', text: 'Went back to the previous network settings.' });
      await refreshAndReset();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  async function onExpired() {
    if (!local && !data?.pending) return;
    setLocal(null);
    setNotice({
      tone: 'warn',
      text: 'The time ran out, so the gateway went back to the previous network settings.',
    });
    try {
      await refreshAndReset();
    } catch {
      // The gateway may still be switching networks; the next poll recovers.
    }
  }

  async function doScan() {
    setScanning(true);
    setScanError(null);
    try {
      setScan(await api.network.wifi());
    } catch (err) {
      setScanError(errorMessage(err));
    } finally {
      setScanning(false);
    }
  }

  async function removeProfile(name: string) {
    setDeleting(null);
    setNotice(null);
    try {
      await api.network.remove(name);
      setNotice({ tone: 'ok', text: `Removed the saved network ${name}.` });
      await status.refresh();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    }
  }

  const activeNames = new Set((data?.devices ?? []).map((d) => d.active).filter(Boolean));
  const savedWifi = (data?.connections ?? []).filter((c) => c.type === 'wifi' && !c.hotspot);
  const conn = data ? CONNECTIVITY[data.connectivity] : null;

  return (
    <>
      <div className="page-header">
        <h1>Network</h1>
        {conn && <Badge tone={conn.tone}>{conn.label}</Badge>}
      </div>
      <div className="stack">
        {pending && (
          <ConfirmBanner
            pending={pending}
            onConfirm={() => void confirm()}
            onUndo={() => void undo()}
            onExpired={() => void onExpired()}
            busy={busy}
          />
        )}
        {notice && (
          <Alert tone={notice.tone} live>
            {notice.text}
          </Alert>
        )}
        {status.error && !data && <Alert tone="bad">{errorMessage(status.error)}</Alert>}
        {data?.hotspot && (
          <Alert tone="info" title="Setup hotspot is on">
            Setup hotspot is on because no known network could be reached. Join the Wi-Fi network{' '}
            <strong>{data.hotspot.ssid}</strong> with your phone or laptop to set up a network
            below.
          </Alert>
        )}

        <Card title="Connections" id="devices">
          {data ? (
            <Table
              caption="Network devices"
              rows={data.devices}
              rowKey={(d) => d.name}
              empty="No network devices found."
              columns={[
                { key: 'name', header: 'Device', render: (d) => d.name },
                { key: 'type', header: 'Type', render: (d) => d.type },
                { key: 'state', header: 'State', render: (d) => d.state },
                {
                  key: 'addr',
                  header: 'Addresses',
                  render: (d) =>
                    d.addresses && d.addresses.length ? (
                      <span className="mono">{d.addresses.join(', ')}</span>
                    ) : (
                      'none'
                    ),
                },
              ]}
            />
          ) : (
            <p className="muted">Loading...</p>
          )}
        </Card>

        {data && eth && (
          <form
            className="stack"
            onSubmit={(e) => {
              e.preventDefault();
              onApplyClick();
            }}
          >
            <Card title="Ethernet" id="ethernet">
              {ethConn ? (
                <div className="stack form-narrow">
                  <RadioGroup
                    legend="How the gateway gets its address"
                    name="eth-method"
                    value={eth.method}
                    onChange={(method) => setEth({ ...eth, method })}
                    options={[
                      { value: 'auto', label: 'Automatic (DHCP)', hint: 'Recommended.' },
                      { value: 'manual', label: 'Fixed address' },
                    ]}
                  />
                  {eth.method === 'manual' && (
                    <>
                      <TextField
                        label="Address with prefix"
                        value={eth.address}
                        onChange={(e) => setEth({ ...eth, address: e.target.value })}
                        placeholder="192.168.1.50/24"
                        hint="The address followed by a slash and the network size, for example 192.168.1.50/24."
                        autoComplete="off"
                      />
                      <TextField
                        label="Gateway (router)"
                        value={eth.gateway}
                        onChange={(e) => setEth({ ...eth, gateway: e.target.value })}
                        placeholder="192.168.1.1"
                        autoComplete="off"
                      />
                    </>
                  )}
                  <TextField
                    label="DNS servers"
                    value={eth.dns}
                    onChange={(e) => setEth({ ...eth, dns: e.target.value })}
                    placeholder="1.1.1.1, 9.9.9.9"
                    hint="Separate several with commas. Leave empty to use the ones from your router."
                    autoComplete="off"
                  />
                  <TextField
                    label="DNS search domains"
                    value={eth.search}
                    onChange={(e) => setEth({ ...eth, search: e.target.value })}
                    placeholder="home.lan"
                    hint="Optional. Separate several with commas."
                    autoComplete="off"
                  />
                </div>
              ) : (
                <p className="muted">This gateway has no Ethernet profile.</p>
              )}
            </Card>

            <Card title="Wi-Fi" id="wifi">
              <div className="stack form-narrow">
                <div className="btn-row">
                  <Button onClick={() => void doScan()} busy={scanning}>
                    Scan for networks
                  </Button>
                </div>
                {scanError && <Alert tone="bad">{scanError}</Alert>}
                {scan && (
                  <p className="muted" role="status">
                    {scan.length === 0
                      ? 'No Wi-Fi networks found.'
                      : `Found ${scan.length} network${scan.length === 1 ? '' : 's'}. Pick one in the name box below.`}
                  </p>
                )}
                <TextField
                  label="Network name (SSID)"
                  value={ssid}
                  onChange={(e) => setSsid(e.target.value)}
                  list="wifi-scan"
                  hint="Pick a scanned network or type a name. It is saved as a new Wi-Fi network when you press Apply."
                  autoComplete="off"
                />
                <datalist id="wifi-scan">
                  {(scan ?? []).map((n) => (
                    <option key={n.ssid} value={n.ssid}>
                      {`${n.signal}% signal${n.secured ? ', password protected' : ', open'}`}
                    </option>
                  ))}
                </datalist>
                <TextField
                  label="Wi-Fi password"
                  type="password"
                  value={psk}
                  onChange={(e) => setPsk(e.target.value)}
                  hint="Leave empty for an open network. The gateway never shows saved passwords."
                  autoComplete="new-password"
                />
                <label className="field-checkbox">
                  <input
                    type="checkbox"
                    checked={hidden}
                    onChange={(e) => setHidden(e.target.checked)}
                  />{' '}
                  This network is hidden
                </label>
              </div>
              <h3>Saved Wi-Fi networks</h3>
              {savedWifi.length === 0 ? (
                <p className="muted">No Wi-Fi networks are saved.</p>
              ) : (
                <Table
                  caption="Saved Wi-Fi networks"
                  rows={savedWifi}
                  rowKey={(c) => c.name}
                  columns={[
                    {
                      key: 'name',
                      header: 'Network',
                      render: (c) => (
                        <>
                          {c.wifi?.ssid ?? c.name}
                          {activeNames.has(c.name) && (
                            <>
                              {' '}
                              <Badge tone="ok">In use</Badge>
                            </>
                          )}
                        </>
                      ),
                    },
                    {
                      key: 'act',
                      header: 'Actions',
                      render: (c) =>
                        deleting === c.name ? (
                          <span className="btn-row">
                            <Button variant="danger" onClick={() => void removeProfile(c.name)}>
                              {`Yes, delete ${c.name}`}
                            </Button>
                            <Button variant="ghost" onClick={() => setDeleting(null)}>
                              Cancel
                            </Button>
                          </span>
                        ) : (
                          <Button
                            variant="danger"
                            disabled={activeNames.has(c.name)}
                            title={
                              activeNames.has(c.name)
                                ? 'This network is in use and cannot be deleted'
                                : undefined
                            }
                            onClick={() => setDeleting(c.name)}
                          >
                            {`Delete ${c.name}`}
                          </Button>
                        ),
                    },
                  ]}
                />
              )}
            </Card>

            <Card title="Host name" id="hostname">
              <div className="form-narrow">
                <TextField
                  label="Host name"
                  value={hostname}
                  onChange={(e) => setHostname(e.target.value)}
                  hint="The name this gateway uses on your network, for example pwsgw."
                  autoComplete="off"
                />
              </div>
            </Card>

            {applyError && (
              <Alert tone="bad" title="The gateway did not accept these settings" live>
                {applyError}
              </Alert>
            )}
            {warnAsk && addressWarning && (
              <Alert tone="warn" title="This changes the address you are using" live>
                <p>{addressWarning}</p>
                <div className="btn-row">
                  <Button variant="danger" onClick={() => void doApply()}>
                    Apply anyway
                  </Button>
                  <Button variant="ghost" onClick={() => setWarnAsk(false)}>
                    Cancel
                  </Button>
                </div>
              </Alert>
            )}
            <div className="btn-row">
              <Button
                type="submit"
                variant="primary"
                busy={busy}
                disabled={changedCount === 0 || !!pending}
              >
                Apply
              </Button>
              {pending && <span className="muted">Confirm or undo the waiting change first.</span>}
              {!pending && changedCount === 0 && (
                <span className="muted">Change something above to enable Apply.</span>
              )}
            </div>
          </form>
        )}

        <Card title="How network changes work" id="howto">
          <ol className="steps">
            <li>You change the address, Wi-Fi or host name and press Apply.</li>
            <li>The gateway switches right away and starts a 90 second timer.</li>
            <li>You open this page from the new address and press Confirm and keep.</li>
            <li>If Confirm does not arrive in time, the old settings come back by themselves.</li>
          </ol>
        </Card>
      </div>
    </>
  );
}
