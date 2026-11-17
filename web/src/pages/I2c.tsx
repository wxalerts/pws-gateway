import { useEffect, useState } from 'react';
import { api, errorMessage, type I2cDevice, type I2cKind } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { Table, type Column } from '../components/Table';
import { formatDateTime } from '../lib/format';
import { usePoll } from '../lib/usePoll';

const KIND_NOTE: Record<I2cKind, string> = {
  ds3231: 'Real time clock',
  bmp280: 'Pressure only. Used as a station.',
  bme280: 'Adds humidity. Detected, but not used yet.',
  bme680: 'Pressure, humidity and gas. Detected.',
  unknown: 'Unknown device',
};

function chipHex(id: number | null): string {
  return id === null ? 'n/a' : `0x${id.toString(16).toUpperCase().padStart(2, '0')}`;
}

export function DeviceTable({ devices }: { devices: I2cDevice[] }) {
  const columns: Column<I2cDevice>[] = [
    { key: 'addr', header: 'Address', render: (d) => <span className="mono">{d.addr_hex}</span> },
    {
      key: 'device',
      header: 'Device',
      render: (d) => (
        <>
          <strong>{d.label}</strong>{' '}
          <Badge tone={d.kind === 'bmp280' ? 'ok' : d.kind === 'unknown' ? 'warn' : 'info'}>
            {d.kind === 'unknown' ? 'unknown' : d.kind.toUpperCase()}
          </Badge>
          <div className="muted">{KIND_NOTE[d.kind]}</div>
        </>
      ),
    },
    {
      key: 'chip',
      header: 'Chip id',
      render: (d) => <span className="mono">{chipHex(d.chip_id)}</span>,
    },
    {
      key: 'note',
      header: 'Note',
      optional: true,
      render: (d) => (d.kernel_driver ? 'in use by the kernel driver' : ''),
    },
  ];
  return (
    <Table
      columns={columns}
      rows={devices}
      rowKey={(d) => d.addr_hex}
      caption="Devices found on the I2C bus"
      empty="No devices found. Check the wiring below."
    />
  );
}

export function I2cPage() {
  const status = usePoll((s) => api.i2c.get({ signal: s }), 10000);
  const [deadline, setDeadline] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'ok' | 'bad'; text: string } | null>(null);

  useEffect(() => {
    document.title = 'I2C | PWS Gateway';
  }, []);

  const data = status.data;
  const serverDeadline = data?.change.phase === 'applied' ? data.change.deadline : null;
  const pendingDeadline = deadline ?? serverDeadline;

  async function run(action: () => Promise<unknown>, okText: string, after?: () => void) {
    setBusy(true);
    setNotice(null);
    try {
      await action();
      after?.();
      setNotice({ tone: 'ok', text: okText });
      await status.refresh();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  async function enable() {
    setBusy(true);
    setNotice(null);
    try {
      const res = await api.i2c.enable(true);
      setDeadline(res.confirm_deadline);
      await status.refresh();
    } catch (err) {
      setNotice({ tone: 'bad', text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="page-header">
        <h1>I2C</h1>
        {data?.enabled && data.bus_present && (
          <Button onClick={() => void status.refresh()}>Scan again</Button>
        )}
      </div>
      <div className="stack">
        {status.error && !data && <Alert tone="bad">{errorMessage(status.error)}</Alert>}
        {pendingDeadline && (
          <Alert tone="warn" title="Reboot required" live>
            <p>
              The I2C change was applied and the gateway needs a reboot to use it. Press Confirm
              before <strong>{formatDateTime(pendingDeadline)}</strong> to keep it. If you do
              nothing, the gateway goes back to the previous setting.
            </p>
            <div className="btn-row">
              <Button
                variant="primary"
                busy={busy}
                onClick={() =>
                  void run(
                    () => api.i2c.confirm(),
                    'I2C setting confirmed and kept.',
                    () => setDeadline(null),
                  )
                }
              >
                Confirm and keep
              </Button>
              <Button
                busy={busy}
                onClick={() =>
                  void run(
                    () => api.i2c.rollback(),
                    'I2C change undone.',
                    () => setDeadline(null),
                  )
                }
              >
                Undo
              </Button>
            </div>
          </Alert>
        )}
        {notice && (
          <Alert tone={notice.tone} live>
            {notice.text}
          </Alert>
        )}
        {data && !data.enabled && (
          <Card title="I2C is off" id="enable">
            <p>
              I2C is the two wire bus used by a real time clock and by pressure sensors such as the
              BMP280. It is off by default. Turning it on changes the boot configuration and needs a
              reboot.
            </p>
            <div className="btn-row">
              <Button variant="primary" busy={busy} onClick={() => void enable()}>
                Enable I2C
              </Button>
            </div>
          </Card>
        )}
        {data?.enabled && !data.bus_present && (
          <Alert tone="warn" title="I2C bus not present" live>
            I2C is enabled but the bus does not exist yet.
            {data.reboot_required ? ' Reboot the gateway to finish turning it on.' : ''}
          </Alert>
        )}
        {data?.enabled && data.bus_present && (
          <Card title="Devices on the bus" id="devices">
            <DeviceTable devices={data.devices} />
          </Card>
        )}
        <Card title="Wiring" id="wiring">
          <ul className="steps">
            <li>3.3 V to pin 1</li>
            <li>SDA to GPIO2, pin 3</li>
            <li>SCL to GPIO3, pin 5</li>
            <li>GND to pin 6</li>
          </ul>
          <p>
            Expected addresses: DS3231 clock at <span className="mono">0x68</span>, BMP280 or BME280
            at <span className="mono">0x76</span> or <span className="mono">0x77</span>. The chip id
            tells them apart: <span className="mono">0x58</span> is a BMP280 and{' '}
            <span className="mono">0x60</span> is a BME280.
          </p>
          <p className="muted">
            Cheap boards are often mislabeled, so trust the chip id over the label on the board.
          </p>
        </Card>
      </div>
    </>
  );
}
