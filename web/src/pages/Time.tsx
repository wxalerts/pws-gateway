import { useEffect } from 'react';
import { Link } from 'react-router-dom';
import { api, errorMessage, type RtcInfo, type TimeStatus } from '../api';
import { Alert } from '../components/Alert';
import { Card } from '../components/Card';
import { Stat, StatGrid } from '../components/Stat';
import { formatDateTime, formatRelative } from '../lib/format';
import { usePoll } from '../lib/usePoll';

const SOURCE_LABEL: Record<TimeStatus['source'], string> = {
  ntp: 'NTP',
  gps: 'GPS',
  lte: 'LTE network',
  rtc: 'RTC',
  none: 'none',
};

export function RtcCard({
  rtc,
  fakeHwclockDisabled,
}: {
  rtc: RtcInfo | null;
  fakeHwclockDisabled: boolean | null;
}) {
  if (!rtc) {
    return (
      <Card title="Real time clock" id="rtc">
        <p>No RTC detected.</p>
        <p className="muted">
          A DS3231 clock keeps time without a network. Turn on and check the bus on the{' '}
          <Link to="/i2c">I2C page</Link>.
        </p>
      </Card>
    );
  }
  return (
    <Card title="Real time clock" id="rtc">
      {rtc.oscillator_stopped && (
        <Alert tone="warn" title="RTC time is not trusted" live>
          The RTC lost power or its oscillator stopped. Its time is not trusted until the system
          clock syncs and the RTC is rewritten.
        </Alert>
      )}
      <dl className="kv">
        <dt>RTC time</dt>
        <dd className="mono">{formatDateTime(rtc.time)}</dd>
        <dt>temperature</dt>
        <dd>{rtc.temp_c === null ? 'unknown' : `${rtc.temp_c.toFixed(1)} C`}</dd>
        <dt>fake-hwclock</dt>
        <dd>
          {fakeHwclockDisabled === null
            ? 'unknown'
            : fakeHwclockDisabled
              ? 'fake-hwclock is disabled, so the RTC is the only offline clock.'
              : 'fake-hwclock is still enabled and may compete with the RTC.'}
        </dd>
      </dl>
    </Card>
  );
}

export function TimePage() {
  const status = usePoll((s) => api.time({ signal: s }), 10000);

  useEffect(() => {
    document.title = 'Time | PWS Gateway';
  }, []);

  const t = status.data;
  return (
    <>
      <div className="page-header">
        <h1>Time</h1>
      </div>
      <div className="stack">
        {status.error && !t && <Alert tone="bad">{errorMessage(status.error)}</Alert>}
        {t && (
          <>
            <StatGrid>
              <Stat label="System time" value={formatDateTime(t.system_time)} />
              <Stat label="Source" value={SOURCE_LABEL[t.source]} />
              <Stat label="Offset" value={t.offset_ms === null ? 'unknown' : `${t.offset_ms} ms`} />
              <Stat label="Last sync" value={t.last_sync ? formatRelative(t.last_sync) : 'never'} />
              <Stat
                label="Synced"
                value={t.synced ? 'yes' : 'no'}
                tone={t.synced ? 'ok' : 'warn'}
              />
            </StatGrid>
            <RtcCard rtc={t.rtc} fakeHwclockDisabled={t.fake_hwclock_disabled} />
          </>
        )}
        {!t && !status.error && <p className="muted">Loading...</p>}
      </div>
    </>
  );
}
