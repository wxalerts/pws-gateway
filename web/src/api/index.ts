// One function per endpoint. Pages import from here and never call fetch.

import { http, type RequestOptions } from './client';
import type {
  ApplyResult,
  Claim,
  Config,
  Credentials,
  Health,
  Radio,
  RadioUpdate,
  Reading,
  Session,
  SetupStatus,
  Station,
  StationUpdate,
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
    putSection: (section: string, value: Record<string, unknown>) =>
      http.put<unknown>(`/api/config/${encodeURIComponent(section)}`, value),
    apply: () => http.post<ApplyResult>('/api/config/apply'),
    confirm: () => http.post<unknown>('/api/config/confirm'),
  },
  claim: {
    get: (opts?: RequestOptions) => http.get<Claim>('/api/claim', opts),
    start: () => http.post<Claim>('/api/claim/start'),
    cancel: () => http.post<Claim>('/api/claim/cancel'),
    unlink: () => http.post<Claim>('/api/claim/unlink'),
  },
  uplink: (opts?: RequestOptions) => http.get<Uplink>('/api/uplink', opts),
  update: (opts?: RequestOptions) => http.get<UpdateStatus>('/api/update', opts),
  logs: (lines: number, opts?: RequestOptions) =>
    http.getText(`/api/logs?lines=${encodeURIComponent(String(lines))}`, opts),
  readings: (station: string, since: Date, opts?: RequestOptions) =>
    http.get<Reading[]>(
      `/api/readings?station=${encodeURIComponent(station)}&since=${encodeURIComponent(since.toISOString())}`,
      opts,
    ),
  version: (opts?: RequestOptions) => http.get<Version>('/api/version', opts),
};
