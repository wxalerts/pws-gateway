// One function per endpoint. Pages import from here and never call fetch.

import { http, type RequestOptions } from './client';
import type {
  ApplyResult,
  Claim,
  Config,
  Credentials,
  CwopConfig,
  Health,
  I2cEnableResult,
  I2cStatus,
  ModemStatus,
  NetworkApplyRequest,
  NetworkApplyResult,
  NetworkStatus,
  UpdateSettings,
  WifiNetwork,
  Radio,
  RadioUpdate,
  RelayStatus,
  Reading,
  Session,
  SetupStatus,
  Station,
  StationUpdate,
  TimeStatus,
  UpdateStatus,
  Uplink,
  Version,
} from './types';

export * from './types';
export { ApiError, errorMessage, isApiError, onUnauthenticated } from './client';

export const api = {
  setup: {
    status: (opts?: RequestOptions) => http.get<SetupStatus>('/api/setup', opts),
    create: (creds: Credentials) => http.post<unknown>('/api/setup', creds),
  },
  session: {
    get: (opts?: RequestOptions) =>
      http.get<Session>('/api/session', { allowUnauthenticated: true, ...opts }),
    login: (creds: Credentials) =>
      http.post<Session>('/api/session', creds, { allowUnauthenticated: true }),
    logout: () => http.delete<unknown>('/api/session'),
  },
  health: (opts?: RequestOptions) => http.get<Health>('/api/health', opts),
  stations: {
    list: (opts?: RequestOptions) => http.get<Station[]>('/api/stations', opts),
    update: (key: string, patch: StationUpdate) =>
      http.put<Station>(`/api/stations/${encodeURIComponent(key)}`, patch),
  },
  radios: {
    list: (opts?: RequestOptions) => http.get<Radio[]>('/api/radios', opts),
    update: (serial: string, patch: RadioUpdate) =>
      http.put<Radio>(`/api/radios/${encodeURIComponent(serial)}`, patch),
  },
  config: {
    get: (opts?: RequestOptions) => http.get<Config>('/api/config', opts),
    composite: async (opts?: RequestOptions): Promise<Record<string, string>> => {
      const cfg = await http.get<Config>('/api/config', opts);
      const c = cfg.composite;
      return c && typeof c === 'object' ? (c as Record<string, string>) : {};
    },
    putSection: (section: string, value: Record<string, unknown>) =>
      http.put<unknown>(`/api/config/${encodeURIComponent(section)}`, value),
    apply: () => http.post<ApplyResult>('/api/config/apply'),
    confirm: () => http.post<unknown>('/api/config/confirm'),
  },
  relay: {
    get: (opts?: RequestOptions) => http.get<RelayStatus>('/api/relay', opts),
    save: (cwop: CwopConfig) =>
      http.put<unknown>('/api/config/relay', { cwop: { ...cwop, credential_file: '' } }),
    setPasscode: (passcode: string) =>
      http.put<{ passcode_set: boolean }>('/api/relay/cwop/passcode', { passcode }),
    clearPasscode: () => http.delete<{ passcode_set: boolean }>('/api/relay/cwop/passcode'),
  },
  claim: {
    get: (opts?: RequestOptions) => http.get<Claim>('/api/claim', opts),
    start: () => http.post<Claim>('/api/claim/start'),
    cancel: () => http.post<Claim>('/api/claim/cancel'),
    unlink: () => http.post<Claim>('/api/claim/unlink'),
  },
  modem: (opts?: RequestOptions) => http.get<ModemStatus>('/api/modem', opts),
  i2c: {
    get: (opts?: RequestOptions) => http.get<I2cStatus>('/api/i2c', opts),
    enable: (enabled: boolean) => http.post<I2cEnableResult>('/api/i2c/enable', { enabled }),
    confirm: () => http.post<unknown>('/api/i2c/confirm'),
    rollback: () => http.post<unknown>('/api/i2c/rollback'),
  },
  time: (opts?: RequestOptions) => http.get<TimeStatus>('/api/time', opts),
  uplink: (opts?: RequestOptions) => http.get<Uplink>('/api/uplink', opts),
  update: (opts?: RequestOptions) => http.get<UpdateStatus>('/api/update', opts),
  updates: {
    check: () => http.post<{ started: boolean }>('/api/update/check'),
    install: () => http.post<{ started: boolean }>('/api/update/apply'),
    saveSettings: (s: UpdateSettings) => http.put<unknown>('/api/config/updates', s),
  },
  network: {
    get: (opts?: RequestOptions) => http.get<NetworkStatus>('/api/network', opts),
    apply: (req: NetworkApplyRequest) => http.post<NetworkApplyResult>('/api/network/apply', req),
    confirm: () => http.post<{ confirmed: boolean }>('/api/network/confirm'),
    revert: () => http.post<{ reverted: boolean }>('/api/network/revert'),
    remove: (name: string) =>
      http.delete<unknown>(`/api/network/connections/${encodeURIComponent(name)}`),
    wifi: (opts?: RequestOptions) => http.get<WifiNetwork[]>('/api/network/wifi', opts),
  },
  logs: (lines: number, opts?: RequestOptions) =>
    http.getText(`/api/logs?lines=${encodeURIComponent(String(lines))}`, opts),
  readings: (station: string, since: Date, opts?: RequestOptions) =>
    http.get<Reading[]>(
      `/api/readings?station=${encodeURIComponent(station)}&since=${encodeURIComponent(since.toISOString())}`,
      opts,
    ),
  version: (opts?: RequestOptions) => http.get<Version>('/api/version', opts),
};
