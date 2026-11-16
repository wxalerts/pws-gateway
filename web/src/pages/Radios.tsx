import { useEffect, useState, type FormEvent } from 'react';
import {
  api,
  errorMessage,
  RADIO_BANDS,
  type Radio,
  type RadioBand,
  type RadioRole,
  type RadioUpdate,
} from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { RadioGroup, SelectField } from '../components/fields';
import { formatBand } from '../lib/format';
import { usePoll } from '../lib/usePoll';

function statusTone(status: string): 'ok' | 'warn' | 'bad' | 'neutral' {
  const s = status.toLowerCase();
  if (s === 'running') return 'ok';
  if (s === 'starting' || s === 'stopping') return 'warn';
  if (s.includes('error') || s === 'failed' || s === 'missing') return 'bad';
  return 'neutral';
}

interface RadioRoleFormProps {
  radio: Radio;
  /** Other radios, used to warn when two dongles tune the same band. */
  others: Radio[];
  onSave: (patch: RadioUpdate) => Promise<void>;
}

/** One dongle: role and band. Exported on its own for tests. */
export function RadioRoleForm({ radio, others, onSave }: RadioRoleFormProps) {
  const [role, setRole] = useState<RadioRole>(radio.role);
  const [band, setBand] = useState<RadioBand>(radio.band_mhz);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const dirty = role !== radio.role || band !== radio.band_mhz;
  const sameBand = others.filter(
    (o) => o.role === 'rtl_433' && o.band_mhz === band && role === 'rtl_433',
  );
  const formId = `radio-${radio.serial}`;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await onSave({ role, band_mhz: band });
      setSaved(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card
      title={radio.product || `Dongle ${radio.serial}`}
      id={formId}
      actions={<Badge tone={statusTone(radio.status)}>{radio.status}</Badge>}
    >
      <dl className="kv">
        <dt>Serial</dt>
        <dd className="mono">{radio.serial}</dd>
        <dt>USB port</dt>
        <dd className="mono">{radio.bus}</dd>
      </dl>
      <form
        onSubmit={submit}
        className="form-narrow"
        aria-label={`Settings for dongle ${radio.serial}`}
      >
        <RadioGroup<RadioRole>
          legend="Role"
          name={`${formId}-role`}
          value={role}
          onChange={setRole}
          options={[
            { value: 'rtl_433', label: 'Receive weather stations', hint: '(rtl_433)' },
            {
              value: 'disabled',
              label: 'Disabled',
              hint: 'The dongle stays plugged in but is not used.',
            },
          ]}
        />
        <SelectField<RadioBand>
          label="Band"
          value={band}
          onChange={setBand}
          options={RADIO_BANDS.map((b) => ({
            value: b,
            label:
              b === 915
                ? '915 MHz (North American stations such as Acurite, Ambient 915)'
                : '433.92 MHz (most stations worldwide)',
          }))}
          hint="A dongle listens to one band at a time. To receive both 433 MHz and 915 MHz sensors you need two dongles, each on its own band."
          disabled={role === 'disabled'}
        />
        {sameBand.length > 0 && (
          <Alert tone="warn" title={`Another dongle already listens on ${formatBand(band)}`}>
            Two dongles on the same band hear the same stations twice and gain nothing. Put one on
            the other band or disable it.
          </Alert>
        )}
        {error && (
          <Alert tone="bad" live>
            {error}
          </Alert>
        )}
        {saved && !error && (
          <Alert tone="ok" live>
            Saved. The radio restarts with the new settings in a few seconds.
          </Alert>
        )}
        <div className="btn-row">
          <Button type="submit" variant="primary" busy={busy} disabled={!dirty}>
            Save
          </Button>
          {dirty && (
            <Button
              onClick={() => {
                setRole(radio.role);
                setBand(radio.band_mhz);
              }}
              disabled={busy}
            >
              Reset
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}

export function RadiosPage() {
  const poll = usePoll((s) => api.radios.list({ signal: s }), 5000);

  useEffect(() => {
    document.title = 'Radios | PWS Gateway';
  }, []);

  const radios = poll.data ?? [];

  return (
    <>
      <div className="page-header">
        <h1>Radios</h1>
        <span className="muted">{radios.length} detected</span>
      </div>
      <div className="stack">
        <p className="muted">
          Each USB radio dongle gets a role and a band. One dongle tunes one band: pick 433.92 MHz
          for most stations, or 915 MHz for North American brands that use it. With two dongles you
          can cover both.
        </p>
        {poll.error && !poll.data && <Alert tone="bad">{errorMessage(poll.error)}</Alert>}
        {poll.data && radios.length === 0 && (
          <Alert tone="warn" title="No radio dongle found">
            Plug an RTL-SDR dongle into the Pi. If it is already plugged in, try another USB port or
            a powered hub, then check the Logs page for USB errors.
          </Alert>
        )}
        {radios.length > 1 && (
          <Alert tone="info">
            Several dongles draw more current than the Pi can supply on its own. Use a powered USB
            hub to avoid low-voltage warnings and dropouts.
          </Alert>
        )}
        <div className="grid">
          {radios.map((r) => (
            <RadioRoleForm
              key={`${r.serial}-${r.role}-${r.band_mhz}`}
              radio={r}
              others={radios.filter((o) => o.serial !== r.serial)}
              onSave={async (patch) => {
                await api.radios.update(r.serial, patch);
                await poll.refresh();
              }}
            />
          ))}
        </div>
      </div>
    </>
  );
}
