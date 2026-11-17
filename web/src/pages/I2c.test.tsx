import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type * as ApiModule from '../api';
import type { I2cStatus } from '../api';
import { I2cPage } from './I2c';

const getFn = vi.fn<() => Promise<I2cStatus>>();
const enableFn = vi.fn();
const confirmFn = vi.fn();
const rollbackFn = vi.fn();

vi.mock('../api', async () => {
  const real = await vi.importActual<typeof ApiModule>('../api');
  return {
    ...real,
    api: {
      ...real.api,
      i2c: {
        get: () => getFn(),
        enable: (e: boolean) => enableFn(e),
        confirm: () => confirmFn(),
        rollback: () => rollbackFn(),
      },
    },
  };
});

afterEach(() => vi.clearAllMocks());

const idle = { phase: 'idle', candidate: null, deadline: null } as const;

const scan: I2cStatus = {
  enabled: true,
  bus_present: true,
  reboot_required: false,
  devices: [
    {
      addr: 0x68,
      addr_hex: '0x68',
      kind: 'ds3231',
      label: 'DS3231 real time clock',
      chip_id: null,
      kernel_driver: true,
    },
    {
      addr: 0x76,
      addr_hex: '0x76',
      kind: 'bmp280',
      label: 'BMP280 pressure sensor',
      chip_id: 0x58,
      kernel_driver: false,
    },
    {
      addr: 0x77,
      addr_hex: '0x77',
      kind: 'bme280',
      label: 'BME280 sensor',
      chip_id: 0x60,
      kernel_driver: false,
    },
  ],
  change: idle,
};

describe('I2cPage', () => {
  it('distinguishes BMP280 from BME280 in the scan table', async () => {
    getFn.mockResolvedValue(scan);
    render(<I2cPage />);
    const bmp = (await screen.findByText('BMP280 pressure sensor')).closest('tr')!;
    expect(within(bmp).getByText(/Pressure only. Used as a station/)).toBeInTheDocument();
    expect(within(bmp).getByText('0x58')).toBeInTheDocument();
    const bme = screen.getByText('BME280 sensor').closest('tr')!;
    expect(within(bme).getByText(/Detected, but not used yet/)).toBeInTheDocument();
    expect(within(bme).getByText('0x60')).toBeInTheDocument();
    const rtc = screen.getByText('DS3231 real time clock').closest('tr')!;
    expect(within(rtc).getByText('in use by the kernel driver')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan again' })).toBeInTheDocument();
    expect(screen.getByText(/mislabeled/)).toBeInTheDocument();
    expect(document.title).toBe('I2C | PWS Gateway');
  });

  it('enables I2C, then shows the reboot banner and confirms', async () => {
    const user = userEvent.setup();
    const off: I2cStatus = { ...scan, enabled: false, bus_present: false, devices: [] };
    getFn.mockResolvedValue(off);
    enableFn.mockResolvedValue({
      applied: true,
      reboot_required: true,
      confirm_deadline: new Date(Date.now() + 300_000).toISOString(),
    });
    confirmFn.mockResolvedValue({ confirmed: true });
    render(<I2cPage />);

    await user.click(await screen.findByRole('button', { name: 'Enable I2C' }));
    expect(enableFn).toHaveBeenCalledWith(true);
    expect(await screen.findByText(/the gateway needs a reboot to use it/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Confirm and keep' }));
    expect(confirmFn).toHaveBeenCalledTimes(1);
    expect(await screen.findByText('I2C setting confirmed and kept.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Undo' })).toBeNull();
  });

  it('undoes a pending change', async () => {
    const user = userEvent.setup();
    getFn.mockResolvedValue({
      ...scan,
      reboot_required: true,
      change: { phase: 'applied', candidate: true, deadline: new Date().toISOString() },
    });
    rollbackFn.mockResolvedValue({ rolled_back: true });
    render(<I2cPage />);
    await user.click(await screen.findByRole('button', { name: 'Undo' }));
    expect(rollbackFn).toHaveBeenCalledTimes(1);
  });
});
