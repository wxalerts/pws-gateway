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

export type RelayOwner = 'device' | 'server' | '';

export interface RelayProblem {
  code: string;
  message: string;
}

export interface RelayHealth {
  last_attempt: string | null;
  last_success: string | null;
  last_error: { code: string; message: string; at: string } | null;
  failures: number;
}

export type RelayState =
  'off' | 'server' | 'invalid' | 'waiting_for_time' | 'ok' | 'error' | 'idle';

export interface CwopConfig {
  owner: RelayOwner;
  station: string;
  id: string;
  server: string;
  position_opt_in: boolean;
  lat: number;
  lon: number;
}

export interface RelayStatus {
  cwop: {
    config: CwopConfig;
    needs_passcode: boolean;
    status: {
      state: RelayState;
      problems: RelayProblem[];
      passcode_set: boolean;
      health: RelayHealth;
      next_attempt: string | null;
    };
  };
}

export interface ModemInfo {
  model: string;
  state: string;
  carrier: string;
  mcc_mnc: string;
  reg: string;
  tech: string;
  rssi_dbm: number | null;
  signal_pct: number | null;
  /** RFC 3339. */
  network_time: string | null;
}

export interface GpsInfo {
  /** 0 none, 2 for 2D, 3 for 3D. */
  fix: 0 | 2 | 3;
  sats: number;
  lat: number | null;
  lon: number | null;
  alt_m: number | null;
}

export type UplinkLink = 'lte' | 'ethernet' | 'wifi' | 'unknown';

export interface ModemStatus {
  modem: ModemInfo | null;
  gps: GpsInfo | null;
  link: UplinkLink;
}

export type I2cKind = 'ds3231' | 'bmp280' | 'bme280' | 'bme680' | 'unknown';

export interface I2cDevice {
  addr: number;
  addr_hex: string;
  kind: I2cKind;
  label: string;
  chip_id: number | null;
  kernel_driver: boolean;
}

export interface I2cChange {
  phase: 'idle' | 'staged' | 'applied';
  candidate: boolean | null;
  deadline: string | null;
}

export interface I2cStatus {
  enabled: boolean;
  bus_present: boolean;
  reboot_required: boolean;
  devices: I2cDevice[];
  change: I2cChange;
}

export interface I2cEnableResult {
  applied: boolean;
  reboot_required: boolean;
  /** RFC 3339. */
  confirm_deadline: string;
}

export type TimeSource = 'ntp' | 'gps' | 'lte' | 'rtc' | 'none';

export interface RtcInfo {
  time: string;
  oscillator_stopped: boolean;
  temp_c: number | null;
}

export interface TimeStatus {
  synced: boolean;
  source: TimeSource;
  offset_ms: number | null;
  last_sync: string | null;
  rtc: RtcInfo | null;
  fake_hwclock_disabled: boolean | null;
  system_time: string;
}
