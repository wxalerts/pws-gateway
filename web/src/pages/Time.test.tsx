import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type * as ApiModule from '../api';
import type { TimeStatus } from '../api';
import { TimePage } from './Time';

const timeFn = vi.fn<() => Promise<TimeStatus>>();

vi.mock('../api', async () => {
  const real = await vi.importActual<typeof ApiModule>('../api');
  return { ...real, api: { ...real.api, time: () => timeFn() } };
});

afterEach(() => vi.clearAllMocks());

const base: TimeStatus = {
  synced: true,
  source: 'ntp',
  offset_ms: 4,
  last_sync: new Date(Date.now() - 60_000).toISOString(),
  rtc: { time: '2026-11-17T12:00:00Z', oscillator_stopped: false, temp_c: 27.5 },
  fake_hwclock_disabled: true,
  system_time: '2026-11-17T12:00:01Z',
};

function renderPage() {
  render(
    <MemoryRouter>
      <TimePage />
    </MemoryRouter>,
  );
}

describe('TimePage', () => {
  it('shows the stat grid and a healthy RTC without a warning', async () => {
    timeFn.mockResolvedValue(base);
    renderPage();
    expect(await screen.findByText('NTP')).toBeInTheDocument();
    expect(screen.getByText('4 ms')).toBeInTheDocument();
    expect(screen.getByText('yes')).toBeInTheDocument();
    expect(screen.getByText(/fake-hwclock is disabled/)).toBeInTheDocument();
    expect(screen.queryByText(/oscillator stopped/)).toBeNull();
    expect(document.title).toBe('Time | PWS Gateway');
  });

  it('warns when the RTC oscillator stopped', async () => {
    timeFn.mockResolvedValue({
      ...base,
      rtc: { ...base.rtc!, oscillator_stopped: true },
    });
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The RTC lost power or its oscillator stopped. Its time is not trusted until the system clock syncs and the RTC is rewritten.',
    );
  });

  it('says no RTC was detected and links to the I2C page', async () => {
    timeFn.mockResolvedValue({ ...base, rtc: null, fake_hwclock_disabled: null });
    renderPage();
    expect(await screen.findByText('No RTC detected.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'I2C page' })).toHaveAttribute('href', '/i2c');
  });
});
