import { useEffect, useState, type FormEvent } from 'react';
import {
  api,
  errorMessage,
  type CwopConfig,
  type RelayState,
  type RelayStatus,
  type Station,
} from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { CheckboxField, NumberField, SelectField, TextField } from '../components/fields';
import { formatDateTime, formatRelative } from '../lib/format';
import { usePoll } from '../lib/usePoll';

const STATE_LABEL: Record<RelayState, string> = {
  off: 'Off',
  server: 'Relayed by WxAlerts',
  invalid: 'Needs attention',
  waiting_for_time: 'Waiting for the clock',
  ok: 'Delivering',
  error: 'Last send failed',
  idle: 'Ready, nothing sent yet',
};

function stateTone(s: RelayState): 'ok' | 'warn' | 'bad' | 'neutral' {
  if (s === 'ok') return 'ok';
  if (s === 'error') return 'bad';
  if (s === 'invalid' || s === 'waiting_for_time') return 'warn';
  return 'neutral';
}

const HAM_ID = /^[A-Z0-9]{1,2}[0-9][A-Z]{1,4}(-[0-9]{1,2})?$/;

interface RelayFormProps {
  config: CwopConfig;
  stations: Pick<Station, 'key' | 'name'>[];
  needsPasscode: boolean;
  passcodeSet: boolean;
  onSave: (config: CwopConfig, passcode: string) => Promise<void>;
}

/** The CWOP settings form. Exported on its own for tests. */
export function RelayForm({
  config,
  stations,
  needsPasscode,
  passcodeSet,
  onSave,
}: RelayFormProps) {
  const [owner, setOwner] = useState<'device' | 'off'>(
    config.owner === 'device' ? 'device' : 'off',
  );
  const [station, setStation] = useState(config.station);
  const [id, setId] = useState(config.id);
  const [optIn, setOptIn] = useState(config.position_opt_in);
  const [lat, setLat] = useState<number | null>(config.lat || null);
  const [lon, setLon] = useState<number | null>(config.lon || null);
  const [passcode, setPasscode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const idUpper = id.trim().toUpperCase();
  const isHam = HAM_ID.test(idUpper);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await onSave(
        {
          owner: owner === 'device' ? 'device' : config.owner === 'server' ? 'server' : '',
          station,
          id: idUpper,
          server: config.server,
          position_opt_in: optIn,
          lat: lat ?? 0,
          lon: lon ?? 0,
        },
        passcode,
      );
      setPasscode('');
      setSaved(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="form-narrow" aria-label="CWOP relay settings">
      <SelectField<'off' | 'device'>
        label="Who sends to CWOP"
        value={owner}
        onChange={setOwner}
        options={[
          { value: 'off', label: 'Nobody from this Pi' },
          { value: 'device', label: 'This Pi, with my own CWOP ID' },
        ]}
        hint="A station is sent from one place only. If WxAlerts relays this station for you, leave this off."
      />
      {owner === 'device' && (
        <>
          <SelectField<string>
            label="Station"
            value={station}
            onChange={setStation}
            options={[
              { value: '', label: 'Choose a station' },
              ...stations.map((s) => ({
                value: s.key,
                label: s.name ? `${s.name} (${s.key})` : s.key,
              })),
            ]}
          />
          <TextField
            label="CWOP ID"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="CW1234"
            autoCapitalize="characters"
            hint="Register your station at wxqa.com first. IDs such as CW1234 need no password."
          />
          {(needsPasscode || isHam) && (
            <TextField
              label="Validation code"
              type="password"
              autoComplete="off"
              value={passcode}
              onChange={(e) => setPasscode(e.target.value)}
              placeholder={passcodeSet ? 'Stored, leave blank to keep it' : ''}
              hint="Amateur callsigns need the code from CWOP support. It stays on this Pi and is never shown again or sent to WxAlerts."
            />
          )}
          <CheckboxField
            label="Publish my station position on CWOP"
            checked={optIn}
            onChange={(e) => setOptIn(e.target.checked)}
            hint="CWOP shows the exact coordinates publicly. Nothing is sent unless you tick this."
          />
          {optIn && (
            <>
              <NumberField
                label="Latitude"
                value={lat}
                onChange={setLat}
                step="any"
                suffix="degrees"
              />
              <NumberField
                label="Longitude"
                value={lon}
                onChange={setLon}
                step="any"
                suffix="degrees"
              />
            </>
          )}
        </>
      )}
      {error && (
        <Alert tone="bad" live>
          {error}
        </Alert>
      )}
      {saved && !error && (
        <Alert tone="ok" live>
          Saved.
        </Alert>
      )}
      <div className="btn-row">
        <Button type="submit" variant="primary" busy={busy}>
          Save
        </Button>
      </div>
    </form>
  );
}

interface DeliveryProps {
  data: RelayStatus['cwop'];
}

/** Delivery state: last success and last error. Exported for tests. */
export function RelayDelivery({ data }: DeliveryProps) {
  const { status } = data;
  const h = status.health;
  return (
    <Card
      title="Delivery"
      id="delivery"
      actions={<Badge tone={stateTone(status.state)}>{STATE_LABEL[status.state]}</Badge>}
    >
      {status.state === 'server' && (
        <p className="muted">WxAlerts sends this station to CWOP. The Pi sends nothing.</p>
      )}
      {status.problems.length > 0 && (
        <Alert tone="warn" title="Fix before sending starts">
          <ul>
            {status.problems.map((p) => (
              <li key={p.code + p.message}>{p.message}</li>
            ))}
          </ul>
        </Alert>
      )}
      <dl className="kv">
        <dt>Last success</dt>
        <dd>
          {h.last_success ? (
            <>
              {formatRelative(h.last_success)}{' '}
              <span className="muted">({formatDateTime(h.last_success)})</span>
            </>
          ) : (
            'Never'
          )}
        </dd>
        <dt>Last error</dt>
        <dd>
          {h.last_error ? (
            <>
              <span className="mono">{h.last_error.code}</span>: {h.last_error.message}{' '}
              <span className="muted">({formatRelative(h.last_error.at)})</span>
            </>
          ) : (
            'None'
          )}
        </dd>
        <dt>Failures in a row</dt>
        <dd>{h.failures}</dd>
        <dt>Next attempt</dt>
        <dd>
          {status.next_attempt
            ? formatDateTime(status.next_attempt)
            : 'At the next closed 5 minute bucket'}
        </dd>
      </dl>
    </Card>
  );
}

export function RelayPage() {
  const poll = usePoll((s) => api.relay.get({ signal: s }), 5000);
  const stations = usePoll((s) => api.stations.list({ signal: s }), 30000);

  useEffect(() => {
    document.title = 'CWOP relay | PWS Gateway';
  }, []);

  const d = poll.data?.cwop;

  return (
    <>
      <div className="page-header">
        <h1>CWOP relay</h1>
      </div>
      <div className="stack">
        <p className="muted">
          Send one station to the Citizen Weather Observer Program with your own CWOP ID. The
          credentials stay on this Pi and work without a WxAlerts account. Pressure is reduced to
          sea level and wind to 10 m before sending. Nothing is sent while the clock is unsynced.
        </p>
        {poll.error && !d && <Alert tone="bad">{errorMessage(poll.error)}</Alert>}
        {d && (
          <div className="grid">
            <Card title="Settings" id="settings">
              <RelayForm
                key={JSON.stringify(d.config)}
                config={d.config}
                stations={stations.data ?? []}
                needsPasscode={d.needs_passcode}
                passcodeSet={d.status.passcode_set}
                onSave={async (cfg, passcode) => {
                  if (passcode) await api.relay.setPasscode(passcode);
                  await api.relay.save(cfg);
                  await poll.refresh();
                }}
              />
            </Card>
            <RelayDelivery data={d} />
          </div>
        )}
      </div>
    </>
  );
}
