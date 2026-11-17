// Vite dev-server middleware that fakes every /api endpoint the Go daemon
// serves. Active only for `vite dev`; the production build never includes it.
//
// It enforces the same rules as the real server where they matter for the UI:
// a session cookie, 401 {error:"unauthenticated"} when logged out, and the
// X-Requested-With: pwsgw header on every mutating request.

import type { IncomingMessage, ServerResponse } from 'node:http';
import type { Plugin } from 'vite';
import {
  CLAIM_APPROVE_AFTER_MS,
  CLAIM_EXPIRES_MS,
  createState,
  recompute,
  toApi,
  uptimeSeconds,
  type MockConnection,
  type MockState,
} from './state';
import { m7Route } from './m7';
import { readingsSince } from './readings';

const COOKIE = 'pwsgw_session';
const CSRF_HEADER = 'x-requested-with';
const CSRF_VALUE = 'pwsgw';

type Json = object | null;

function send(res: ServerResponse, status: number, body: Json | string, type = 'application/json') {
  res.statusCode = status;
  res.setHeader('Content-Type', type.startsWith('text/') ? `${type}; charset=utf-8` : type);
  res.setHeader('Cache-Control', 'no-store');
  res.end(typeof body === 'string' ? body : JSON.stringify(body));
}

function fail(res: ServerResponse, status: number, error: string, detail?: string) {
  send(res, status, detail ? { error, detail } : { error });
}

function readBody(req: IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8');
      if (!raw) return resolve(null);
      try {
        resolve(JSON.parse(raw));
      } catch {
        reject(new Error('invalid json'));
      }
    });
    req.on('error', reject);
  });
}

function cookieValue(req: IncomingMessage, name: string): string | null {
  const header = req.headers.cookie ?? '';
  for (const part of header.split(';')) {
    const [k, ...rest] = part.trim().split('=');
    if (k === name) return decodeURIComponent(rest.join('='));
  }
  return null;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

export function mockApi(): Plugin {
  const state = createState();
  let scenarioTick = 0;

  return {
    name: 'pwsgw-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url ?? '/', 'http://localhost');
        if (!url.pathname.startsWith('/api/')) return next();

        const method = (req.method ?? 'GET').toUpperCase();
        const mutating = method !== 'GET' && method !== 'HEAD';

        // Simulate a little network latency so loading states are visible.
        await sleep(80 + Math.random() * 120);

        if (mutating && req.headers[CSRF_HEADER] !== CSRF_VALUE) {
          return fail(res, 403, 'csrf', `Missing X-Requested-With: ${CSRF_VALUE} header`);
        }

        let body: unknown = null;
        if (mutating) {
          try {
            body = await readBody(req);
          } catch {
            return fail(res, 400, 'bad_request', 'Body is not valid JSON');
          }
        }

        try {
          await route(state, method, url, body, req, res, () => scenarioTick++);
        } catch (err) {
          fail(res, 500, 'internal', err instanceof Error ? err.message : String(err));
        }
      });
    },
  };
}

async function route(
  state: MockState,
  method: string,
  url: URL,
  body: unknown,
  req: IncomingMessage,
  res: ServerResponse,
  tick: () => number,
) {
  const path = url.pathname;
  const token = cookieValue(req, COOKIE);
  const authed = token !== null && state.sessions.has(token);

  // Unauthenticated endpoints.
  if (path === '/api/setup') {
    if (method === 'GET') return send(res, 200, { needs_setup: state.needsSetup });
    if (method === 'POST') {
      if (!state.needsSetup)
        return fail(res, 409, 'already_setup', 'Setup has already been completed');
      if (
        !isObject(body) ||
        typeof body.username !== 'string' ||
        typeof body.password !== 'string'
      ) {
        return fail(res, 400, 'bad_request', 'username and password are required');
      }
      if (body.password.length < 8) {
        return fail(res, 400, 'weak_password', 'Password must be at least 8 characters');
      }
      state.user = { username: body.username, password: body.password };
      state.needsSetup = false;
      return send(res, 200, { ok: true });
    }
  }

  if (path === '/api/session') {
    if (method === 'POST') {
      if (state.needsSetup) return fail(res, 409, 'needs_setup', 'Create the admin account first');
      if (
        !isObject(body) ||
        typeof body.username !== 'string' ||
        typeof body.password !== 'string'
      ) {
        return fail(res, 400, 'bad_request', 'username and password are required');
      }
      if (
        !state.user ||
        body.username !== state.user.username ||
        body.password !== state.user.password
      ) {
        return fail(res, 401, 'invalid_credentials', 'Username or password is wrong');
      }
      const t = Math.random().toString(36).slice(2) + Date.now().toString(36);
      state.sessions.add(t);
      res.setHeader('Set-Cookie', `${COOKIE}=${t}; Path=/; HttpOnly; SameSite=Strict`);
      return send(res, 200, { authenticated: true, username: state.user.username });
    }
    if (method === 'DELETE') {
      if (token) state.sessions.delete(token);
      res.setHeader('Set-Cookie', `${COOKIE}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`);
      return send(res, 200, { authenticated: false });
    }
    if (method === 'GET') {
      if (!authed) return fail(res, 401, 'unauthenticated');
      return send(res, 200, { authenticated: true, username: state.user?.username ?? 'admin' });
    }
  }

  if (!authed) return fail(res, 401, 'unauthenticated');

  const now = Date.now();

  if (path === '/api/health' && method === 'GET') {
    const t = tick();
    // Every so often show the kind of warning a weak power supply produces.
    const underVoltageNow = t % 23 === 5;
    return send(res, 200, {
      fw: '0.3.1',
      board: {
        model: 'Raspberry Pi 4 Model B Rev 1.4',
        soc: 'BCM2711',
        arch: 'arm64',
        tier: 'full',
      },
      cpu_temp_c: Math.round((51.2 + 3 * Math.sin(now / 120_000)) * 10) / 10,
      throttled: {
        under_voltage_now: underVoltageNow,
        under_voltage_occurred: true,
        freq_capped: false,
        throttled: false,
        soft_temp_limit: false,
      },
      mem_free_mb: Math.round(2410 + 60 * Math.sin(now / 50_000)),
      uptime_s: uptimeSeconds(state),
      rootfs_used_pct: 23,
      tmpfs_used_pct: 4,
      time: { synced: true, src: 'ntp', offset_ms: -3 },
      outbox: outbox(now),
      usb_resets: 2,
    });
  }

  if (path === '/api/stations' && method === 'GET') {
    for (const s of state.stations) {
      // Keep the active stations looking live.
      if (s.n_sightings > 100) s.last_seen = new Date(now - (now % 16_000)).toISOString();
    }
    return send(res, 200, state.stations.map(toApi));
  }

  const stationMatch = path.match(/^\/api\/stations\/(.+)$/);
  if (stationMatch && method === 'PUT') {
    const key = decodeURIComponent(stationMatch[1]);
    const s = state.stations.find((x) => x.key === key);
    if (!s) return fail(res, 404, 'not_found', `No station with key ${key}`);
    if (!isObject(body)) return fail(res, 400, 'bad_request', 'Body must be an object');
    if (typeof body.selected === 'boolean') {
      s.selected = body.selected;
    }
    if (body.fields === null) s.overrides = {};
    else if (isObject(body.fields)) {
      for (const [k, v] of Object.entries(body.fields)) {
        if (typeof v === 'boolean') s.overrides[k] = v;
      }
    }
    if (body.include_raw === null || typeof body.include_raw === 'boolean') {
      s.includeRawOverride = body.include_raw;
    }
    recompute(s);
    if (typeof body.name === 'string') s.name = body.name.slice(0, 64);
    if (body.alt_m === null || typeof body.alt_m === 'number') s.alt_m = body.alt_m;
    if (body.wind_height_m === null || typeof body.wind_height_m === 'number') {
      s.wind_height_m = body.wind_height_m;
    }
    return send(res, 200, toApi(s));
  }

  if (path === '/api/radios' && method === 'GET') return send(res, 200, state.radios);

  const radioMatch = path.match(/^\/api\/radios\/([^/]+)$/);
  if (radioMatch && method === 'PUT') {
    const r = state.radios.find((x) => x.serial === radioMatch[1]);
    if (!r) return fail(res, 404, 'not_found', `No radio with serial ${radioMatch[1]}`);
    if (!isObject(body)) return fail(res, 400, 'bad_request', 'Body must be an object');
    if (body.role !== 'rtl_433' && body.role !== 'disabled') {
      return fail(res, 400, 'bad_request', 'role must be rtl_433 or disabled');
    }
    if (body.band_mhz !== 433.92 && body.band_mhz !== 915) {
      return fail(res, 400, 'bad_request', 'band_mhz must be 433.92 or 915');
    }
    r.role = body.role;
    r.band_mhz = body.band_mhz;
    r.status = r.role === 'disabled' ? 'idle' : 'starting';
    setTimeout(() => {
      if (r.role === 'rtl_433') r.status = 'running';
    }, 4000);
    return send(res, 200, r);
  }

  if (path === '/api/config' && method === 'GET') {
    return send(res, 200, {
      ...state.config,
      pending_confirm: state.pendingConfirm,
    });
  }
  const sectionMatch = path.match(/^\/api\/config\/([a-z_]+)$/);
  if (sectionMatch && method === 'PUT') {
    const section = sectionMatch[1];
    if (section === 'apply' || section === 'confirm') return fail(res, 405, 'method_not_allowed');
    if (!isObject(body)) return fail(res, 400, 'bad_request', 'Body must be an object');
    state.config[section] = { ...(state.config[section] ?? {}), ...body };
    return send(res, 200, state.config[section]);
  }
  if (path === '/api/config/apply' && method === 'POST') {
    const deadline = new Date(now + 90_000).toISOString();
    state.pendingConfirm = deadline;
    setTimeout(() => {
      if (state.pendingConfirm === deadline) state.pendingConfirm = null;
    }, 90_000);
    return send(res, 200, { applied: true, pending_confirm: true, confirm_deadline: deadline });
  }
  if (path === '/api/config/confirm' && method === 'POST') {
    if (!state.pendingConfirm)
      return fail(res, 409, 'nothing_pending', 'There is no change waiting for confirmation');
    state.pendingConfirm = null;
    return send(res, 200, { confirmed: true });
  }

  if (path === '/api/claim' && method === 'GET') return send(res, 200, claimBody(state));
  if (path === '/api/claim/start' && method === 'POST') {
    if (state.claim.state === 'claimed') {
      return fail(res, 409, 'already_linked', 'This gateway is already linked. Unlink it first.');
    }
    state.claim = { state: 'pending', startedAt: Date.now(), error: null, deviceId: null };
    return send(res, 200, claimBody(state));
  }
  if (path === '/api/claim/cancel' && method === 'POST') {
    if (state.claim.state === 'pending') {
      state.claim = { state: 'unclaimed', startedAt: 0, error: null, deviceId: null };
    }
    return send(res, 200, claimBody(state));
  }
  if (path === '/api/claim/unlink' && method === 'POST') {
    state.claim = { state: 'unclaimed', startedAt: 0, error: null, deviceId: null };
    return send(res, 200, claimBody(state));
  }

  if (path === '/api/uplink' && method === 'GET') {
    const linked = claimBody(state).state === 'claimed';
    const connected = linked && Math.floor(now / 300_000) % 7 !== 3; // drop out for five minutes now and then
    return send(res, 200, {
      connected,
      broker: linked ? 'wss://pws-mqtt.wxalerts.org/mqtt' : '',
      device_id: linked ? (state.claim.deviceId ?? '') : '',
      last_publish: new Date(now - (now % 900_000) - 12_000).toISOString(),
      outbox: outbox(now),
    });
  }

  if (path === '/api/update' && method === 'GET') return send(res, 200, updateBody(state, now));
  if ((path === '/api/update/check' || path === '/api/update/apply') && method === 'POST') {
    if (state.updateRunAt !== null) return fail(res, 409, 'busy', 'An update is already running');
    state.updateCheckedAt = now;
    if (path === '/api/update/apply') state.updateRunAt = now;
    return send(res, 202, { started: true });
  }

  if (path.startsWith('/api/network')) return network(state, method, path, body, res, now);

  if (path === '/api/logs' && method === 'GET') {
    const lines = Math.min(2000, Math.max(1, Number(url.searchParams.get('lines') ?? 200) || 200));
    return send(res, 200, fakeLogs(lines, now), 'text/plain');
  }

  if (path === '/api/readings' && method === 'GET') {
    const key = url.searchParams.get('station') ?? '';
    const s = state.stations.find((x) => x.key === key);
    if (!s) return fail(res, 404, 'not_found', `No station with key ${key}`);
    const sinceRaw = url.searchParams.get('since');
    const since = sinceRaw ? Date.parse(sinceRaw) : now - 3_600_000;
    if (Number.isNaN(since)) return fail(res, 400, 'bad_request', 'since must be RFC 3339');
    return send(res, 200, readingsSince(s, since, now));
  }

  const m7 = m7Route(path, method, body, now);
  if (m7) return send(res, m7.status, m7.body);

  if (path === '/api/version' && method === 'GET') {
    return send(res, 200, {
      version: '0.3.1',
      commit: 'a1b2c3d',
      build_date: '2026-11-10T14:02:11Z',
    });
  }

  if (path === '/api/health' || path === '/api/stations' || path === '/api/radios') {
    return fail(res, 405, 'method_not_allowed');
  }
  return fail(res, 404, 'not_found', `No route for ${method} ${path}`);
}

function updateBody(state: MockState, now: number) {
  const u = state.update;
  const updates = state.config.updates as { window: string; channel: string };
  const extra: Record<string, unknown> = {};
  if (state.updateRunAt !== null) {
    // "Install now": download for 8s, restart for 8s (the API keeps answering here).
    const el = (now - state.updateRunAt) / 1000;
    if (el < 8) {
      Object.assign(u, { state: 'downloading', target: '0.3.2', error: null });
      extra.progress = Math.round((el / 8) * 100);
    } else if (el < 16) {
      Object.assign(u, { state: 'rebooting', target: '0.3.2', error: null });
    } else {
      Object.assign(u, { state: 'good', current: '0.3.2', target: null, error: null });
      state.updateRunAt = null;
    }
    u.ts = new Date(now).toISOString();
  } else {
    // Walk through a believable update cycle every few minutes.
    const phase = Math.floor(now / 60_000) % 12;
    if (phase === 0) Object.assign(u, { state: 'idle', target: null, error: null });
    else if (phase === 1) {
      Object.assign(u, { state: 'downloading', target: '0.3.2', error: null });
      extra.progress = Math.round(((now % 60_000) / 60_000) * 100);
    } else if (phase === 2) Object.assign(u, { state: 'staged', target: '0.3.2' });
    else if (phase === 3) {
      Object.assign(u, { state: 'deferred', target: '0.3.2' });
      extra.defer_kind = 'window';
      extra.detail = 'Next update window opens at 02:00.';
      extra.os_deferred = true;
    } else if (phase === 4)
      Object.assign(u, {
        state: 'rolled_back',
        target: '0.3.2',
        error: 'health check failed after reboot',
      });
    else if (phase === 5) {
      Object.assign(u, { state: 'failed', target: '0.3.2', error: 'signature did not verify' });
      extra.code = 'bad_signature';
    } else Object.assign(u, { state: 'good', target: null, error: null });
    u.ts = new Date(now - (now % 60_000)).toISOString();
  }
  return {
    ...u,
    ...extra,
    last_check: new Date(state.updateCheckedAt).toISOString(),
    window: updates.window,
    channel: updates.channel,
    enabled: process.env.MOCK_UPDATES_OFF !== '1',
  };
}

function networkBody(state: MockState) {
  const n = state.net;
  const eth = n.connections.find((c) => c.type === 'ethernet');
  const wifi = n.connections.find((c) => c.name === 'HomeWiFi');
  const ethAddr = eth?.ipv4.method === 'manual' ? eth.ipv4.address : '192.168.1.42/24';
  return {
    devices: [
      {
        name: 'eth0',
        type: 'ethernet',
        state: 'connected',
        active: eth?.name,
        addresses: [ethAddr],
        mac: 'dc:a6:32:00:11:22',
      },
      {
        name: 'wlan0',
        type: 'wifi',
        state: 'connected',
        active: wifi?.name,
        addresses: ['192.168.1.43/24'],
        mac: 'dc:a6:32:00:11:23',
      },
    ],
    connections: n.connections,
    connectivity: 'full',
    hostname: n.hostname,
    ...(n.pending
      ? {
          pending: {
            deadline: new Date(n.pending.deadline).toISOString(),
            changed: n.pending.changed,
          },
        }
      : {}),
    ...(process.env.MOCK_HOTSPOT === '1' ? { hotspot: { ssid: 'pwsgw-setup-4F2A' } } : {}),
  };
}

function revertNet(state: MockState) {
  const p = state.net.pending;
  if (!p) return;
  clearTimeout(p.timer);
  state.net.connections = p.before.connections;
  state.net.hostname = p.before.hostname;
  state.net.pending = null;
}

function network(
  state: MockState,
  method: string,
  path: string,
  body: unknown,
  res: ServerResponse,
  now: number,
) {
  const n = state.net;
  if (path === '/api/network' && method === 'GET') return send(res, 200, networkBody(state));
  if (path === '/api/network/wifi' && method === 'GET') {
    return send(res, 200, [
      { ssid: 'HomeWiFi', signal: 82, secured: true },
      { ssid: 'Neighbor 5G', signal: 47, secured: true },
      { ssid: 'CoffeeShop Guest', signal: 33, secured: false },
    ]);
  }
  if (path === '/api/network/apply' && method === 'POST') {
    if (n.pending) return fail(res, 409, 'commit_in_progress');
    if (!isObject(body) || !Array.isArray(body.connections)) {
      return fail(res, 422, 'invalid_network', 'connections must be a list');
    }
    const before = {
      connections: structuredClone(n.connections),
      hostname: n.hostname,
    };
    const changed: string[] = [];
    for (const c of body.connections as MockConnection[] & { wifi?: { psk?: string } }[]) {
      if (typeof c.name !== 'string' || !c.name) {
        return fail(res, 422, 'invalid_network', 'every connection needs a name');
      }
      if (c.ipv4?.method === 'manual') {
        if (!c.ipv4.address || !/^\d+\.\d+\.\d+\.\d+\/\d+$/.test(c.ipv4.address)) {
          return fail(
            res,
            422,
            'invalid_network',
            `${c.name}: address must look like 192.168.1.50/24`,
          );
        }
      }
      if (c.type === 'wifi' && c.wifi) {
        // The passphrase is write only: take it and never keep or return it.
        delete (c.wifi as { psk?: string }).psk;
      }
      const i = n.connections.findIndex((x) => x.name === c.name);
      if (i >= 0) n.connections[i] = c;
      else n.connections.push(c);
      changed.push(c.name);
    }
    if (typeof body.hostname === 'string' && body.hostname) {
      if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/i.test(body.hostname)) {
        n.connections = before.connections;
        return fail(res, 422, 'invalid_network', 'host name may only use letters, digits and -');
      }
      n.hostname = body.hostname;
      changed.push('hostname');
    }
    const deadline = now + 90_000;
    n.pending = {
      deadline,
      changed,
      before,
      timer: setTimeout(() => revertNet(state), 90_000),
    };
    return send(res, 200, {
      applied: true,
      confirm_deadline: new Date(deadline).toISOString(),
      changed,
    });
  }
  if (path === '/api/network/confirm' && method === 'POST') {
    if (!n.pending) return fail(res, 409, 'nothing_pending');
    if (now > n.pending.deadline) return fail(res, 410, 'deadline_passed');
    clearTimeout(n.pending.timer);
    n.pending = null;
    return send(res, 200, { confirmed: true });
  }
  if (path === '/api/network/revert' && method === 'POST') {
    revertNet(state);
    return send(res, 200, { reverted: true });
  }
  const del = path.match(/^\/api\/network\/connections\/(.+)$/);
  if (del && method === 'DELETE') {
    const name = decodeURIComponent(del[1]);
    const c = n.connections.find((x) => x.name === name);
    if (!c) return fail(res, 404, 'not_found');
    if (c.type === 'ethernet' || c.name === 'HomeWiFi') {
      return fail(res, 409, 'in_use', `${name} is in use`);
    }
    n.connections = n.connections.filter((x) => x.name !== name);
    return send(res, 200, { deleted: true });
  }
  return fail(res, 404, 'not_found', `No route for ${method} ${path}`);
}

/** Advance the mock claim (approval, expiry) and return the API body. */
function claimBody(state: MockState) {
  const c = state.claim;
  const now = Date.now();
  if (c.state === 'pending') {
    if (now - c.startedAt >= CLAIM_EXPIRES_MS) {
      state.claim = { state: 'unclaimed', startedAt: 0, error: 'expired', deviceId: null };
    } else if (
      process.env.MOCK_CLAIM_NEVER !== '1' &&
      now - c.startedAt >= CLAIM_APPROVE_AFTER_MS
    ) {
      state.claim = { state: 'claimed', startedAt: 0, error: null, deviceId: 'd_7f3a9c2b1k' };
    }
  }
  const s = state.claim;
  const pending = s.state === 'pending';
  return {
    state: s.state,
    user_code: pending ? 'K7MP-2XQD' : null,
    verification_uri: pending ? 'https://wxalerts.org/pws/claim' : null,
    verification_uri_complete: pending ? 'https://wxalerts.org/pws/claim?code=K7MP2XQD' : null,
    expires_at: pending ? new Date(s.startedAt + CLAIM_EXPIRES_MS).toISOString() : null,
    device_id: s.state === 'claimed' ? s.deviceId : null,
    error: s.error,
  };
}

function outbox(now: number) {
  // Depth rises when the mock uplink is "down" and drains afterwards.
  const connected = Math.floor(now / 300_000) % 7 !== 3;
  const depth = connected ? 0 : Math.floor((now % 300_000) / 15_000);
  return { depth, oldest_s: depth === 0 ? 0 : depth * 15 };
}

function fakeLogs(lines: number, now: number): string {
  const msgs = [
    'collector: rtl_433 pid 812 started on 433.920 MHz (serial 00000001)',
    'collector: sighting Fineoffset-WH65B id=49 rssi=-62.1 snr=21.4',
    'store: wrote 16 readings, wal checkpoint in 14m',
    'uplink: published bucket 15m depth=0',
    'health: cpu 51.3C mem_free 2410MB throttled=0x50000',
    'collector: sighting Acurite-5n1 id=3146 ch=A rssi=-78.6 snr=12.2',
    'time: ntp synced offset=-3ms src=pool.ntp.org',
    'update: manifest unchanged (0.3.1)',
    'web: GET /api/health 200 3ms',
  ];
  const out: string[] = [];
  for (let i = lines - 1; i >= 0; i--) {
    const ts = new Date(now - i * 7_000).toISOString();
    const level = i % 37 === 0 ? 'WARN' : 'INFO';
    const msg =
      i % 37 === 0
        ? 'uplink: broker unreachable, holding 3 buckets in outbox'
        : msgs[i % msgs.length];
    out.push(`${ts} ${level} ${msg}`);
  }
  return out.join('\n') + '\n';
}
