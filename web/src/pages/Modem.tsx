import { useEffect } from 'react';
import { api, errorMessage, type GpsInfo, type ModemInfo } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Card } from '../components/Card';
import type { Tone } from '../components/Stat';
import { usePoll } from '../lib/usePoll';

/** Signal tone from RSSI in dBm. Higher (closer to zero) is better. */
function rssiTone(dbm: number | null): Tone {
  if (dbm === null) return 'neutral';
  if (dbm >= -85) return 'ok';
  if (dbm >= -100) return 'warn';
  return 'bad';
}

function rssiLabel(dbm: number | null): string {
  if (dbm === null) return 'unknown';
  if (dbm >= -85) return 'good';
  if (dbm >= -100) return 'fair';
  return 'weak';
}

const FIX_LABEL: Record<number, string> = { 0: 'None', 2: '2D', 3: '3D' };

export function ModemCard({ modem }: { modem: ModemInfo | null }) {
  if (!modem) {
    return (
      <Card title="Cellular modem" id="modem">
        <p>No modem detected.</p>
        <p className="muted">
          Plug in a USB LTE modem to give the gateway a cellular connection. It appears here a few
          seconds after it is connected.
        </p>
      </Card>
    );
  }
  return (
    <Card title="Cellular modem" id="modem" actions={<Badge>{modem.state}</Badge>}>
      <dl className="kv">
        <dt>model</dt>
        <dd>{modem.model}</dd>
        <dt>carrier</dt>
        <dd>{modem.carrier || 'unknown'}</dd>
        <dt>MCC-MNC</dt>
        <dd className="mono">{modem.mcc_mnc || 'unknown'}</dd>
        <dt>registration</dt>
        <dd>{modem.reg}</dd>
        <dt>technology</dt>
        <dd>{modem.tech}</dd>
        <dt>RSSI</dt>
        <dd>
          {modem.rssi_dbm === null ? (
            'unknown'
          ) : (
            <>
              <span className="mono">{modem.rssi_dbm} dBm</span>{' '}
              <Badge tone={rssiTone(modem.rssi_dbm)}>{rssiLabel(modem.rssi_dbm)}</Badge>
            </>
          )}
        </dd>
        <dt>signal</dt>
        <dd>{modem.signal_pct === null ? 'unknown' : `${modem.signal_pct}%`}</dd>
      </dl>
    </Card>
  );
}

export function GpsCard({ gps }: { gps: GpsInfo | null }) {
  if (!gps) {
    return (
      <Card title="GPS" id="gps">
        <p>No GPS receiver detected.</p>
        <p className="muted">
          A GPS receiver gives the gateway a position and a trusted time source.
        </p>
      </Card>
    );
  }
  const position =
    gps.lat !== null && gps.lon !== null ? `${gps.lat.toFixed(5)}, ${gps.lon.toFixed(5)}` : null;
  return (
    <Card
      title="GPS"
      id="gps"
      actions={<Badge tone={gps.fix === 0 ? 'warn' : 'ok'}>{`Fix: ${FIX_LABEL[gps.fix]}`}</Badge>}
    >
      <dl className="kv">
        <dt>fix</dt>
        <dd>{FIX_LABEL[gps.fix] ?? 'unknown'}</dd>
        <dt>satellites</dt>
        <dd>{gps.sats}</dd>
        <dt>position</dt>
        <dd className="mono">{position ?? 'no fix yet'}</dd>
        <dt>altitude</dt>
        <dd>{gps.alt_m === null ? 'no fix yet' : `${Math.round(gps.alt_m)} m`}</dd>
      </dl>
      <p className="muted">
        The exact position stays on the gateway. The public map shows a fuzzed location.
      </p>
    </Card>
  );
}

export function ModemPage() {
  const status = usePoll((s) => api.modem({ signal: s }), 10000);

  useEffect(() => {
    document.title = 'Modem and GPS | PWS Gateway';
  }, []);

  const data = status.data;
  return (
    <>
      <div className="page-header">
        <h1>Modem and GPS</h1>
      </div>
      <div className="stack">
        {status.error && !data && <Alert tone="bad">{errorMessage(status.error)}</Alert>}
        {data && (
          <Alert tone={data.link === 'lte' ? 'warn' : 'info'} title="Active uplink" live>
            {data.link === 'lte' ? (
              <p>
                The gateway is sending data over <strong>LTE</strong>. Cellular data may be metered,
                so check your plan.
              </p>
            ) : (
              <p>
                The gateway is using <strong>{data.link}</strong> for its uplink.
              </p>
            )}
          </Alert>
        )}
        {data ? (
          <ModemCard modem={data.modem} />
        ) : (
          !status.error && <p className="muted">Loading...</p>
        )}
        {data && <GpsCard gps={data.gps} />}
      </div>
    </>
  );
}
