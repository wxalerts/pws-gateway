import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type * as ApiModule from '../api';
import type { ModemStatus } from '../api';
import { ModemPage } from './Modem';

const modemFn = vi.fn<() => Promise<ModemStatus>>();

vi.mock('../api', async () => {
  const real = await vi.importActual<typeof ApiModule>('../api');
  return { ...real, api: { ...real.api, modem: () => modemFn() } };
});

afterEach(() => vi.clearAllMocks());

const present: ModemStatus = {
  modem: {
    model: 'Quectel EC25',
    state: 'registered',
    carrier: 'Example Wireless',
    mcc_mnc: '310-260',
    reg: 'home',
    tech: 'LTE',
    rssi_dbm: -81,
    signal_pct: 68,
    network_time: null,
  },
  gps: { fix: 3, sats: 9, lat: 30.12345, lon: -88.54321, alt_m: 12.4 },
  link: 'lte',
};

describe('ModemPage', () => {
  it('shows the modem, a good RSSI badge and the lte uplink note', async () => {
    modemFn.mockResolvedValue(present);
    render(<ModemPage />);
    expect(await screen.findByText('Example Wireless')).toBeInTheDocument();
    expect(screen.getByText('310-260')).toBeInTheDocument();
    expect(screen.getByText('-81 dBm')).toBeInTheDocument();
    expect(screen.getByText('good')).toBeInTheDocument();
    expect(screen.getByText('68%')).toBeInTheDocument();
    expect(screen.getByText(/sending data over/)).toBeInTheDocument();
    expect(document.title).toBe('Modem and GPS | PWS Gateway');
  });

  it('shows a 3D fix with position and the privacy note', async () => {
    modemFn.mockResolvedValue(present);
    render(<ModemPage />);
    expect(await screen.findByText('30.12345, -88.54321')).toBeInTheDocument();
    expect(screen.getByText('3D', { selector: 'dd' })).toBeInTheDocument();
    expect(screen.getByText('9')).toBeInTheDocument();
    expect(screen.getByText(/public map shows a fuzzed location/)).toBeInTheDocument();
  });

  it('shows empty states when there is no modem or GPS', async () => {
    modemFn.mockResolvedValue({ modem: null, gps: null, link: 'ethernet' });
    render(<ModemPage />);
    expect(await screen.findByText('No modem detected.')).toBeInTheDocument();
    expect(screen.getByText(/Plug in a USB LTE modem/)).toBeInTheDocument();
    expect(screen.getByText('No GPS receiver detected.')).toBeInTheDocument();
    expect(screen.queryByText(/sending data over/)).toBeNull();
  });
});
