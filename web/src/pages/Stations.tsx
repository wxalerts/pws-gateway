import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, type Station, type StationUpdate } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { NumberField, TextField } from '../components/fields';
import { Table, type Column } from '../components/Table';
import { formatRelative } from '../lib/format';
import { convert, fieldMeta } from '../lib/units';
import { useUnits } from '../lib/UnitsContext';
import { usePoll } from '../lib/usePoll';

function signalTone(rssi: number): 'ok' | 'warn' | 'bad' {
  if (rssi > -70) return 'ok';
  if (rssi > -85) return 'warn';
  return 'bad';
}

function staleTone(lastSeen: string): 'ok' | 'warn' | 'bad' {
  const age = (Date.now() - Date.parse(lastSeen)) / 1000;
  if (age < 300) return 'ok';
  if (age < 3600) return 'warn';
  return 'bad';
}

interface StationsTableProps {
  stations: Station[];
  onSelect: (station: Station) => void;
  onEdit: (station: Station) => void;
  busyKey?: string | null;
}

/** Discovered stations. Exported on its own so it can be tested without the page. */
export function StationsTable({ stations, onSelect, onEdit, busyKey }: StationsTableProps) {
  const columns: Column<Station>[] = [
    {
      key: 'station',
      header: 'Station',
      render: (s) => (
        <>
          <div>
            <Link to={`/stations/${encodeURIComponent(s.key)}`}>{s.name || s.model}</Link>{' '}
            {s.selected && <Badge tone="info">Selected for upload</Badge>}
          </div>
          <div className="muted mono">
            {s.model} id {s.id}
            {s.channel !== null && s.channel !== undefined ? ` ch ${s.channel}` : ''}
          </div>
        </>
      ),
    },
    {
      key: 'last_seen',
      header: 'Last heard',
      render: (s) => <Badge tone={staleTone(s.last_seen)}>{formatRelative(s.last_seen)}</Badge>,
    },
    {
      key: 'rssi',
      header: 'Signal',
      numeric: true,
      render: (s) => (
        <span
          className={`tone-${signalTone(s.rssi_db)}`}
          title={`RSSI ${s.rssi_db} dB, SNR ${s.snr_db} dB`}
        >
          {s.rssi_db.toFixed(0)} dB
          <span className="muted"> / {s.snr_db.toFixed(0)}</span>
        </span>
      ),
    },
    {
      key: 'battery',
      header: 'Battery',
      render: (s) => (
        <Badge tone={s.battery_ok ? 'ok' : 'warn'}>{s.battery_ok ? 'OK' : 'Low'}</Badge>
      ),
    },
    {
      key: 'fields',
      header: 'Fields',
      optional: true,
      render: (s) =>
        s.fields.length === 0 ? (
          <span className="muted">none</span>
        ) : (
          <span
            className={`short-labels${s.selected ? '' : ' muted'}`}
            title={`${s.selected ? 'Used' : 'Would be used'}: ${s.fields.map((f) => fieldMeta(f).label).join(', ')}`}
          >
            {s.fields.map((f) => fieldMeta(f).short).join(' ')}
          </span>
        ),
    },
    {
      key: 'n',
      header: 'Sightings',
      numeric: true,
      optional: true,
      render: (s) => s.n_sightings.toLocaleString(),
    },
    {
      key: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      render: (s) => (
        <div className="row-actions">
          {!s.selected && (
            <Button
              variant="primary"
              onClick={() => onSelect(s)}
              busy={busyKey === s.key}
              aria-label={`Select ${s.name || s.model} for upload`}
            >
              Select
            </Button>
          )}
          <Button onClick={() => onEdit(s)} aria-label={`Edit ${s.name || s.model}`}>
            Edit
          </Button>
        </div>
      ),
    },
  ];

  return (
    <Table
      columns={columns}
      rows={stations}
      rowKey={(s) => s.key}
      caption="Weather stations heard by this gateway"
      rowClass={(s) => (s.selected ? 'selected' : undefined)}
      empty="No stations heard yet. The gateway lists a station after three consistent sightings, which can take a few minutes."
    />
  );
}

interface StationEditorProps {
  station: Station;
  onSave: (patch: StationUpdate) => Promise<void>;
  onCancel: () => void;
}

export function StationEditor({ station, onSave, onCancel }: StationEditorProps) {
  const { system } = useUnits();
  const [name, setName] = useState(station.name);
  const [alt, setAlt] = useState<number | null>(station.alt_m);
  const [windH, setWindH] = useState<number | null>(station.wind_height_m);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const feet = (m: number | null) => (m === null ? null : convert(m, 'length', 'us').toFixed(0));

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await onSave({ name: name.trim(), alt_m: alt, wind_height_m: windH });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={`Edit ${station.name || station.model}`} id="station-editor">
      <form onSubmit={submit} className="form-narrow">
        <TextField
          label="Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={64}
          hint="Shown in this UI only. Leave blank to use the model name."
        />
        <NumberField
          label="Altitude above sea level"
          value={alt}
          onChange={setAlt}
          suffix="m"
          step={1}
          min={-500}
          max={9000}
          hint={
            <>
              In metres. Needed to turn station pressure into sea-level pressure.
              {system === 'us' && alt !== null && ` That is about ${feet(alt)} ft.`}
            </>
          }
        />
        <NumberField
          label="Anemometer height"
          value={windH}
          onChange={setWindH}
          suffix="m"
          step={0.1}
          min={0}
          max={100}
          hint={
            <>
              Height of the wind sensor above the ground, in metres. Standard is 10 m; most garden
              mounts are 2 to 4 m.
              {system === 'us' && windH !== null && ` That is about ${feet(windH)} ft.`}
            </>
          }
        />
        {error && (
          <Alert tone="bad" live>
            {error}
          </Alert>
        )}
        <div className="btn-row">
          <Button type="submit" variant="primary" busy={busy}>
            Save
          </Button>
          <Button onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
        </div>
      </form>
    </Card>
  );
}

export function StationsPage() {
  const poll = usePoll((s) => api.stations.list({ signal: s }), 10000);
  const [editing, setEditing] = useState<Station | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ tone: 'ok' | 'bad'; text: string } | null>(null);

  useEffect(() => {
    document.title = 'Stations | PWS Gateway';
  }, []);

  async function select(station: Station) {
    setBusyKey(station.key);
    setNotice(null);
    try {
      await api.stations.update(station.key, { selected: true });
      setNotice({
        tone: 'ok',
        text: `${station.name || station.model} is selected with its default fields. Open the station to change which fields are used.`,
      });
      await poll.refresh();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusyKey(null);
    }
  }

  async function save(patch: StationUpdate) {
    if (!editing) return;
    await api.stations.update(editing.key, patch);
    setNotice({ tone: 'ok', text: 'Station details saved.' });
    setEditing(null);
    await poll.refresh();
  }

  const stations = poll.data ?? [];
  const sorted = [...stations].sort((a, b) => {
    if (a.selected !== b.selected) return a.selected ? -1 : 1;
    return Date.parse(b.last_seen) - Date.parse(a.last_seen);
  });

  return (
    <>
      <div className="page-header">
        <h1>Stations</h1>
        <span className="muted">{stations.length} heard</span>
      </div>
      <div className="stack">
        <p className="muted">
          Everything the radio can decode shows up here, including neighbours' sensors. Select the
          stations you want to use. Only selected stations, and the fields you tick for each, are
          stored and uploaded to WxAlerts. You can select more than one.
        </p>
        {notice && (
          <Alert tone={notice.tone} live>
            {notice.text}
          </Alert>
        )}
        {poll.error && !poll.data && <Alert tone="bad">{errorMessage(poll.error)}</Alert>}
        {editing && (
          <StationEditor
            key={editing.key}
            station={editing}
            onSave={save}
            onCancel={() => setEditing(null)}
          />
        )}
        <Card id="stations">
          {poll.loading && !poll.data ? (
            <p className="muted">Loading...</p>
          ) : (
            <StationsTable
              stations={sorted}
              onSelect={select}
              onEdit={setEditing}
              busyKey={busyKey}
            />
          )}
        </Card>
      </div>
    </>
  );
}
