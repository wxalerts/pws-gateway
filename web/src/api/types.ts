// Types for the JSON the Go daemon serves. Keep these aligned with the API
// contract documented in web/README.md. Units are SI everywhere; conversion
// for display happens in src/lib/units.ts.

export interface ApiErrorBody {
  error: string;
  detail?: string;
}

export interface SetupStatus {
  needs_setup: boolean;
}

export interface Session {
  authenticated: boolean;
  username: string;
}

export interface Credentials {
  username: string;
  password: string;
}

export interface Throttled {
  under_voltage_now: boolean;
  under_voltage_occurred: boolean;
  freq_capped: boolean;
  throttled: boolean;
  soft_temp_limit: boolean;
}

export interface Outbox {
  depth: number;
  oldest_s: number;
}

export interface Health {
  fw: string;
  board: { model: string; soc: string; arch: string; tier: string };
  cpu_temp_c: number;
  throttled: Throttled;
  mem_free_mb: number;
  uptime_s: number;
  rootfs_used_pct: number;
  tmpfs_used_pct: number;
  time: { synced: boolean; src: string; offset_ms: number };
  outbox: Outbox;
  usb_resets: number;
}

export interface Station {
  key: string;
  model: string;
  id: number;
  channel: string | null;
  last_seen: string;
  rssi_db: number;
  snr_db: number;
  /** null when the station never reported a battery flag. */
  battery_ok: boolean | null;
  /** Canonical fields that are stored and uploaded (or would be, if selected). */
  fields: string[];
  /** Every canonical field this station has reported. */
  fields_available: string[];
  /** Decoder fields with no canonical name that this station has reported. */
  raw_fields: string[];
  /** The model default for the available fields. */
  fields_default: string[];
  /** Whether unmapped decoder fields are uploaded under "raw". */
  include_raw: boolean;
  n_sightings: number;
  selected: boolean;
  name: string;
  alt_m: number | null;
  wind_height_m: number | null;
}

export interface StationUpdate {
  selected?: boolean;
  name?: string;
  alt_m?: number | null;
  wind_height_m?: number | null;
  /** Partial: name to on/off. null clears every override (model default). */
  fields?: Record<string, boolean> | null;
  /** null returns to the default (on once the station is selected). */
  include_raw?: boolean | null;
}

export type RadioRole = 'rtl_433' | 'disabled';
export type RadioBand = 433.92 | 915;

export const RADIO_ROLES: RadioRole[] = ['rtl_433', 'disabled'];
export const RADIO_BANDS: RadioBand[] = [433.92, 915];

export interface Radio {
  serial: string;
  bus: string;
  product: string;
  role: RadioRole;
  band_mhz: RadioBand;
  status: string;
}

export interface RadioUpdate {
  role: RadioRole;
  band_mhz: RadioBand;
}

export type Config = Record<string, unknown>;

export interface ApplyResult {
  applied: boolean;
  pending_confirm: boolean;
  confirm_deadline: string;
}

export type ClaimState = 'unclaimed' | 'pending' | 'claimed';

export interface Claim {
  state: ClaimState;
  /** The 8 character code with a dash in the middle, while pending. */
  user_code: string | null;
  verification_uri: string | null;
  /** Same address with the code filled in, for the QR code. */
  verification_uri_complete: string | null;
  expires_at: string | null;
  device_id: string | null;
  /** Why the last claim ended: expired, denied or failed. */
  error: 'expired' | 'denied' | 'failed' | null;
}

export interface Uplink {
  connected: boolean;
  broker: string;
  device_id: string;
  last_publish: string | null;
  outbox: Outbox;
}

export type UpdateState =
  'idle' | 'downloading' | 'staged' | 'rebooting' | 'good' | 'rolled_back' | 'failed' | 'deferred';

export interface UpdateStatus {
  current: string;
  target: string | null;
  state: UpdateState;
  error: string | null;
  ts: string;
}

export interface Reading {
  ts: string;
  f: Record<string, number>;
}

export interface Version {
  version: string;
  commit: string;
  build_date: string;
}
