// In-memory state for the mock API. Everything here is fake and only exists so
// the UI can be developed without a Pi. Shapes follow the API contract in the
// web/README.md; keep them in sync with src/api/types.ts.

export interface MockStation {
  key: string;
  model: string;
  id: number;
  channel: number | null;
  last_seen: string;
  rssi_db: number;
  snr_db: number;
  battery_ok: boolean;
  /** Effective selected set. Derived from the three members below. */
  fields: string[];
  fields_available: string[];
  raw_fields: string[];
  fields_default: string[];
  include_raw: boolean;
  n_sightings: number;
  selected: boolean;
  name: string;
  alt_m: number | null;
  wind_height_m: number | null;
}

/** What the mock keeps beside the API shape: the owner's overrides. */
export interface MockStationState extends MockStation {
  overrides: Record<string, boolean>;
  includeRawOverride: boolean | null;
}

/** Fields a model reports badly and that stay off by default (mirrors the Go table loosely). */
const OFF_BY_DEFAULT = ['battery_ok'];

type Seed = Omit<MockStation, 'fields_available' | 'raw_fields' | 'fields_default' | 'include_raw'>;

function withSelection(s: Seed): MockStationState {
  const st: MockStationState = {
    ...s,
    fields_available: s.fields,
    raw_fields: ['mic', 'mod'],
    fields_default: s.fields.filter((f) => !OFF_BY_DEFAULT.includes(f)),
    include_raw: false,
    overrides: {},
    includeRawOverride: null,
  };
  recompute(st);
  return st;
}

/** The same rule as config.Station.EffectiveFields and RawIncluded in the Go code. */
export function recompute(s: MockStationState): void {
  const on = new Set(s.fields_default);
  for (const [f, v] of Object.entries(s.overrides)) {
    if (v) on.add(f);
    else on.delete(f);
  }
  s.fields = [...on];
  s.include_raw = s.includeRawOverride ?? s.selected;
}

/** The API shape of a station, without the mock's bookkeeping. */
export function toApi(s: MockStationState): MockStation {
  const { overrides: _o, includeRawOverride: _r, ...api } = s;
  void _o;
  void _r;
  return api;
}

export interface MockRadio {
  serial: string;
  bus: string;
  product: string;
  role: 'rtl_433' | 'disabled';
  band_mhz: 433.92 | 915;
  status: string;
}

export type UpdateState =
  'idle' | 'downloading' | 'staged' | 'rebooting' | 'good' | 'rolled_back' | 'failed' | 'deferred';

export interface MockState {
  needsSetup: boolean;
  user: { username: string; password: string } | null;
  sessions: Set<string>;
  bootedAt: number;
  stations: MockStationState[];
  radios: MockRadio[];
  config: Record<string, Record<string, unknown>>;
  pendingConfirm: string | null;
  update: {
    current: string;
    target: string | null;
    state: UpdateState;
    error: string | null;
    ts: string;
  };
}

function minutesAgo(min: number): string {
  return new Date(Date.now() - min * 60_000).toISOString();
}

export function createState(): MockState {
  const needsSetup = process.env.MOCK_NEEDS_SETUP === '1';
  return {
    needsSetup,
    user: needsSetup ? null : { username: 'admin', password: 'admin' },
    sessions: new Set(),
    bootedAt: Date.now() - 3 * 86_400_000 - 4_321_000,
    stations: (
      [
        {
          key: 'Fineoffset-WH65B:0x31',
          model: 'Fineoffset-WH65B',
          id: 49,
          channel: null,
          last_seen: minutesAgo(0.3),
          rssi_db: -62.1,
          snr_db: 21.4,
          battery_ok: true,
          fields: [
            'temp_c',
            'humidity_pct',
            'wind_speed_ms',
            'wind_gust_ms',
            'wind_dir_deg',
            'rain_mm',
            'uv_index',
            'light_lux',
            'battery_ok',
          ],
          n_sightings: 4812,
          selected: true,
          name: 'Back garden',
          alt_m: 42,
          wind_height_m: 3.5,
        },
        {
          key: 'Acurite-5n1:0x0C4A:A',
          model: 'Acurite-5n1',
          id: 3146,
          channel: 1,
          last_seen: minutesAgo(1.1),
          rssi_db: -78.6,
          snr_db: 12.2,
          battery_ok: true,
          fields: [
            'temp_c',
            'humidity_pct',
            'wind_speed_ms',
            'wind_dir_deg',
            'rain_mm',
            'battery_ok',
          ],
          n_sightings: 1930,
          selected: false,
          name: '',
          alt_m: null,
          wind_height_m: null,
        },
        {
          key: 'Oregon-THGR122N:0x8A:1',
          model: 'Oregon-THGR122N',
          id: 138,
          channel: 1,
          last_seen: minutesAgo(14),
          rssi_db: -89.4,
          snr_db: 6.8,
          battery_ok: false,
          fields: ['temp_c', 'humidity_pct', 'battery_ok'],
          n_sightings: 61,
          selected: false,
          name: '',
          alt_m: null,
          wind_height_m: null,
        },
      ] as Seed[]
    ).map(withSelection),
    radios: [
      {
        serial: '00000001',
        bus: '1-1.2',
        product: 'RTL2838UHIDIR (Nooelec NESDR SMArt v5)',
        role: 'rtl_433',
        band_mhz: 433.92,
        status: 'running',
      },
      {
        serial: '00000002',
        bus: '1-1.3',
        product: 'RTL2838UHIDIR (RTL-SDR Blog V4)',
        role: 'disabled',
        band_mhz: 915,
        status: 'idle',
      },
    ],
    config: {
      network: { hostname: 'pwsgw', dhcp: true, address: null, gateway: null, dns: [] },
      homeassistant: { enabled: false, discovery_prefix: 'homeassistant' },
    },
    pendingConfirm: null,
    update: {
      current: '0.3.1',
      target: null,
      state: 'idle',
      error: null,
      ts: minutesAgo(37),
    },
  };
}

export function uptimeSeconds(state: MockState): number {
  return Math.floor((Date.now() - state.bootedAt) / 1000);
}
