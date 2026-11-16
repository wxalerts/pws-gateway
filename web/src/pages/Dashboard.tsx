import { useEffect } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, type Health, type UpdateState } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Card } from '../components/Card';
import { Stat, StatGrid, type Tone } from '../components/Stat';
import { formatDuration, formatMb, formatPercent, formatRelative } from '../lib/format';
import { formatValue } from '../lib/units';
import { useUnits } from '../lib/UnitsContext';
import { usePoll } from '../lib/usePoll';

export const UPDATE_STATE_LABEL: Record<UpdateState, { label: string; tone: Tone }> = {
  idle: { label: 'Up to date', tone: 'ok' },
  downloading: { label: 'Downloading', tone: 'info' },
  staged: { label: 'Ready to install', tone: 'info' },
  rebooting: { label: 'Restarting', tone: 'info' },
  good: { label: 'Up to date', tone: 'ok' },
  rolled_back: { label: 'Rolled back', tone: 'warn' },
  failed: { label: 'Failed', tone: 'bad' },
  deferred: { label: 'Waiting for a quiet moment', tone: 'neutral' },
};

function cpuTone(c: number): Tone {
  if (c >= 80) return 'bad';
  if (c >= 70) return 'warn';
  return 'ok';
}

export function PowerWarnings({ health }: { health: Health }) {
  const t = health.throttled;
  const items: { tone: Tone; title: string; body: string }[] = [];
  if (t.under_voltage_now) {
    items.push({
      tone: 'bad',
      title: 'Low voltage right now',
      body: 'The Pi is not getting enough power. Radio dongles draw more than most phone chargers can supply. Use the official power supply and plug the dongles into a powered USB hub.',
    });
  } else if (t.under_voltage_occurred) {
    items.push({
      tone: 'warn',
      title: 'Low voltage was seen since the last restart',
      body: 'The power dipped at some point. If this keeps appearing, move the radio dongles to a powered USB hub and check the power supply and cable.',
    });
  }
  if (t.throttled || t.freq_capped) {
    items.push({
      tone: 'warn',
      title: 'The processor is being slowed down',
      body: 'This happens when the Pi runs hot or its power is weak. Give it some airflow or add a heatsink.',
    });
  }
  if (t.soft_temp_limit) {
    items.push({
      tone: 'warn',
      title: 'Temperature limit reached',
      body: 'The Pi is near its soft temperature limit. Move it somewhere cooler or improve ventilation.',
    });
  }
  if (!health.time.synced) {
    items.push({
      tone: 'warn',
      title: 'Clock not synced',
      body: 'Readings are held back until the time is trusted. This normally resolves within a minute of the network coming up.',
    });
  }
  if (health.usb_resets > 5) {
    items.push({
      tone: 'warn',
      title: `${health.usb_resets} USB resets since start`,
      body: 'A dongle keeps disconnecting. A powered hub or a shorter, better cable usually fixes it.',
    });
  }
  if (items.length === 0) return null;
  return (
    <div className="stack">
      {items.map((it) => (
        <Alert key={it.title} tone={it.tone} title={it.title} live>
          {it.body}
        </Alert>
      ))}
    </div>
  );
}

export function DashboardPage() {
  const { system } = useUnits();
  const health = usePoll((s) => api.health({ signal: s }), 5000);
  const uplink = usePoll((s) => api.uplink({ signal: s }), 15000);
  const update = usePoll((s) => api.update({ signal: s }), 30000);
  const stations = usePoll((s) => api.stations.list({ signal: s }), 15000);
  const claim = usePoll((s) => api.claim.get({ signal: s }), 15000);

  useEffect(() => {
    document.title = 'Dashboard | PWS Gateway';
  }, []);

  const h = health.data;
  const selected = stations.data?.find((s) => s.selected) ?? null;

  return (
    <>
      <div className="page-header">
        <h1>Dashboard</h1>
        <span className="muted" role="status" aria-live="polite">
          {health.updatedAt
            ? `Updated ${formatRelative(new Date(health.updatedAt).toISOString())}`
            : 'Loading...'}
        </span>
      </div>

      {health.error && !h && <Alert tone="bad">{errorMessage(health.error)}</Alert>}

      <div className="stack">
        {claim.data && claim.data.state !== 'claimed' && (
          <Alert tone="info" title="This gateway is not linked to WxAlerts yet">
            It is collecting readings, but nothing is sent until you link it.{' '}
            <Link to="/account">
              {claim.data.state === 'pending' ? 'Show the code' : 'Link it to your account'}
            </Link>
          </Alert>
        )}
        {h && <PowerWarnings health={h} />}

        <div className="grid">
          <Card title="Pi health" id="health">
            {h ? (
              <StatGrid>
                <Stat
                  label="CPU temperature"
                  value={formatValue(h.cpu_temp_c, 'temperature', system, 0)}
                  tone={cpuTone(h.cpu_temp_c)}
                />
                <Stat
                  label="Free memory"
                  value={formatMb(h.mem_free_mb)}
                  tone={h.mem_free_mb < 150 ? 'warn' : 'neutral'}
                />
                <Stat label="Uptime" value={formatDuration(h.uptime_s)} />
                <Stat
                  label="Time sync"
                  value={h.time.synced ? 'Synced' : 'Not synced'}
                  hint={h.time.synced ? `${h.time.src}, ${h.time.offset_ms} ms off` : h.time.src}
                  tone={h.time.synced ? 'ok' : 'warn'}
                />
                <Stat
                  label="Storage used"
                  value={formatPercent(h.rootfs_used_pct)}
                  hint={`temp files ${formatPercent(h.tmpfs_used_pct)}`}
                  tone={
                    h.rootfs_used_pct > 90 ? 'bad' : h.rootfs_used_pct > 80 ? 'warn' : 'neutral'
                  }
                />
                <Stat
                  label="USB resets"
                  value={h.usb_resets}
                  tone={h.usb_resets > 5 ? 'warn' : 'neutral'}
                />
                <Stat
                  label="Power"
                  value={
                    h.throttled.under_voltage_now
                      ? 'Low'
                      : h.throttled.under_voltage_occurred
                        ? 'Dipped'
                        : 'Good'
                  }
                  tone={
                    h.throttled.under_voltage_now
                      ? 'bad'
                      : h.throttled.under_voltage_occurred
                        ? 'warn'
                        : 'ok'
                  }
                />
              </StatGrid>
            ) : (
              <p className="muted">Loading...</p>
            )}
            {h && (
              <p className="muted">
                {h.board.model} ({h.board.arch}), firmware {h.fw}
              </p>
            )}
          </Card>

          <Card
            title="Uplink to WxAlerts"
            id="uplink"
            actions={
              uplink.data && (
                <Badge tone={uplink.data.connected ? 'ok' : 'warn'}>
                  {uplink.data.connected ? 'Connected' : 'Offline'}
                </Badge>
              )
            }
          >
            {uplink.data ? (
              <StatGrid>
                <Stat label="Last upload" value={formatRelative(uplink.data.last_publish)} />
                <Stat
                  label="Waiting to send"
                  value={uplink.data.outbox.depth}
                  hint={
                    uplink.data.outbox.depth > 0
                      ? `oldest ${formatDuration(uplink.data.outbox.oldest_s)}`
                      : 'queue empty'
                  }
                  tone={uplink.data.outbox.depth > 20 ? 'warn' : 'neutral'}
                />
              </StatGrid>
            ) : (
              <p className="muted">{uplink.error ? errorMessage(uplink.error) : 'Loading...'}</p>
            )}
            {uplink.data && !uplink.data.connected && (
              <p className="muted">
                Readings are stored on the Pi and sent when the connection returns. Nothing is lost.
              </p>
            )}
            <p>
              <Link to="/uplink">Uplink details</Link>
            </p>
          </Card>

          <Card
            title="Software update"
            id="update"
            actions={
              update.data && (
                <Badge tone={UPDATE_STATE_LABEL[update.data.state]?.tone ?? 'neutral'}>
                  {UPDATE_STATE_LABEL[update.data.state]?.label ?? update.data.state}
                </Badge>
              )
            }
          >
            {update.data ? (
              <StatGrid>
                <Stat label="Installed" value={update.data.current} />
                <Stat label="Available" value={update.data.target ?? 'none'} />
              </StatGrid>
            ) : (
              <p className="muted">{update.error ? errorMessage(update.error) : 'Loading...'}</p>
            )}
            {update.data?.error && (
              <Alert tone="warn" title="Last update problem">
                {update.data.error}
              </Alert>
            )}
            <p>
              <Link to="/updates">Update details</Link>
            </p>
          </Card>

          <Card title="Your station" id="station">
            {stations.data ? (
              selected ? (
                <>
                  <StatGrid>
                    <Stat
                      label="Name"
                      value={selected.name || selected.model}
                      hint={selected.key}
                    />
                    <Stat label="Last heard" value={formatRelative(selected.last_seen)} />
                    <Stat
                      label="Signal"
                      value={`${selected.rssi_db.toFixed(0)} dB`}
                      hint={`SNR ${selected.snr_db.toFixed(0)} dB`}
                    />
                    <Stat
                      label="Battery"
                      value={selected.battery_ok ? 'OK' : 'Low'}
                      tone={selected.battery_ok ? 'ok' : 'warn'}
                    />
                  </StatGrid>
                  <p>
                    <Link to={`/stations/${encodeURIComponent(selected.key)}`}>
                      Live readings and charts
                    </Link>
                  </p>
                </>
              ) : (
                <>
                  <p>No station is selected for upload yet.</p>
                  <p>
                    {stations.data.length > 0
                      ? `${stations.data.length} station${stations.data.length === 1 ? '' : 's'} heard so far. `
                      : 'Nothing heard yet. Stations appear after three consistent sightings. '}
                    <Link to="/stations">Choose a station</Link>
                  </p>
                </>
              )
            ) : (
              <p className="muted">
                {stations.error ? errorMessage(stations.error) : 'Loading...'}
              </p>
            )}
          </Card>
        </div>
      </div>
    </>
  );
}
