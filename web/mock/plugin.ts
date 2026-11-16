// Vite dev-server middleware that fakes every /api endpoint the Go daemon
// serves. Active only for `vite dev`; the production build never includes it.
//
// It enforces the same rules as the real server where they matter for the UI:
// a session cookie, 401 {error:"unauthenticated"} when logged out, and the
// X-Requested-With: pwsgw header on every mutating request.

import type { IncomingMessage, ServerResponse } from 'node:http';
import type { Plugin } from 'vite';
import { createState, uptimeSeconds, type MockState } from './state';
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
    return send(res, 200, state.stations);
  }

  const stationMatch = path.match(/^\/api\/stations\/(.+)$/);
  if (stationMatch && method === 'PUT') {
    const key = decodeURIComponent(stationMatch[1]);
    const s = state.stations.find((x) => x.key === key);
    if (!s) return fail(res, 404, 'not_found', `No station with key ${key}`);
    if (!isObject(body)) return fail(res, 400, 'bad_request', 'Body must be an object');
    if (typeof body.selected === 'boolean') {
      if (body.selected) for (const other of state.stations) other.selected = false;
      s.selected = body.selected;
    }
    if (typeof body.name === 'string') s.name = body.name.slice(0, 64);
    if (body.alt_m === null || typeof body.alt_m === 'number') s.alt_m = body.alt_m;
    if (body.wind_height_m === null || typeof body.wind_height_m === 'number') {
      s.wind_height_m = body.wind_height_m;
    }
    return send(res, 200, s);
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

  if (path === '/api/uplink' && method === 'GET') {
    const connected = Math.floor(now / 300_000) % 7 !== 3; // drop out for five minutes now and then
    return send(res, 200, {
      connected,
      broker: 'mqtts://ingest.wxalerts.org:8883',
      device_id: 'pws-7f3a9c',
      last_publish: new Date(now - (now % 900_000) - 12_000).toISOString(),
      outbox: outbox(now),
    });
  }

  if (path === '/api/update' && method === 'GET') {
    // Walk through a believable update cycle every few minutes.
    const phase = Math.floor(now / 60_000) % 12;
    const u = state.update;
    if (phase === 0) Object.assign(u, { state: 'idle', target: null, error: null });
    else if (phase === 1) Object.assign(u, { state: 'downloading', target: '0.3.2', error: null });
    else if (phase === 2) Object.assign(u, { state: 'staged', target: '0.3.2' });
    else if (phase === 3) Object.assign(u, { state: 'deferred', target: '0.3.2' });
    else if (phase === 4)
      Object.assign(u, {
        state: 'rolled_back',
        target: '0.3.2',
        error: 'health check failed after reboot',
      });
    else Object.assign(u, { state: 'good', target: null, error: null });
    u.ts = new Date(now - (now % 60_000)).toISOString();
    return send(res, 200, u);
  }

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
