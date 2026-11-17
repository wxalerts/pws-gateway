// Mock state and handlers for /api/modem, /api/i2c and /api/time.

const i2c = {
  enabled: true,
  phase: 'idle' as 'idle' | 'staged' | 'applied',
  candidate: null as boolean | null,
  deadline: null as string | null,
};

export interface M7Result {
  status: number;
  body: object;
}

export function m7Route(path: string, method: string, body: unknown, now: number): M7Result | null {
  if (path === '/api/modem' && method === 'GET') {
    return {
      status: 200,
      body: {
        modem: {
          model: 'Quectel EC25',
          state: 'registered',
          carrier: 'Example Wireless',
          mcc_mnc: '310-260',
          reg: 'home',
          tech: 'LTE',
          rssi_dbm: -81,
          signal_pct: 68,
          network_time: new Date(now).toISOString(),
        },
        gps: { fix: 0, sats: 3, lat: null, lon: null, alt_m: null },
        link: 'ethernet',
      },
    };
  }
  if (path === '/api/i2c' && method === 'GET') {
    return {
      status: 200,
      body: {
        enabled: i2c.enabled,
        bus_present: i2c.enabled && i2c.phase !== 'applied',
        reboot_required: i2c.phase === 'applied',
        devices: i2c.enabled
          ? [
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
            ]
          : [],
        change: { phase: i2c.phase, candidate: i2c.candidate, deadline: i2c.deadline },
      },
    };
  }
  if (path === '/api/i2c/enable' && method === 'POST') {
    const b = body as { enabled?: unknown } | null;
    if (!b || typeof b.enabled !== 'boolean') {
      return { status: 400, body: { error: 'bad_request', detail: 'enabled must be a boolean' } };
    }
    const deadline = new Date(now + 300_000).toISOString();
    i2c.phase = 'applied';
    i2c.candidate = b.enabled;
    i2c.deadline = deadline;
    setTimeout(() => {
      if (i2c.deadline === deadline) {
        i2c.phase = 'idle';
        i2c.candidate = null;
        i2c.deadline = null;
      }
    }, 300_000);
    return {
      status: 200,
      body: { applied: true, reboot_required: true, confirm_deadline: deadline },
    };
  }
  if (path === '/api/i2c/confirm' && method === 'POST') {
    if (i2c.phase !== 'applied') {
      return {
        status: 409,
        body: { error: 'nothing_pending', detail: 'No I2C change is waiting' },
      };
    }
    if (i2c.candidate !== null) i2c.enabled = i2c.candidate;
    i2c.phase = 'idle';
    i2c.candidate = null;
    i2c.deadline = null;
    return { status: 200, body: { confirmed: true } };
  }
  if (path === '/api/i2c/rollback' && method === 'POST') {
    if (i2c.phase !== 'applied') {
      return {
        status: 409,
        body: { error: 'nothing_pending', detail: 'No I2C change is waiting' },
      };
    }
    i2c.phase = 'idle';
    i2c.candidate = null;
    i2c.deadline = null;
    return { status: 200, body: { rolled_back: true } };
  }
  if (path === '/api/time' && method === 'GET') {
    return {
      status: 200,
      body: {
        synced: true,
        source: 'ntp',
        offset_ms: 4,
        last_sync: new Date(now - 240_000).toISOString(),
        rtc: { time: new Date(now - 1000).toISOString(), oscillator_stopped: false, temp_c: 27.5 },
        fake_hwclock_disabled: true,
        system_time: new Date(now).toISOString(),
      },
    };
  }
  return null;
}
