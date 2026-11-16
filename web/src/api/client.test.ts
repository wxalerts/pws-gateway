import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, CSRF_HEADER, CSRF_VALUE, http, onUnauthenticated } from './client';
import { api } from './index';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('api client', () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    onUnauthenticated(null);
  });

  it('sends the CSRF header and JSON body on mutating requests', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { ok: true }));

    await http.put('/api/radios/abc', { role: 'rtl_433', band_mhz: 915 });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/radios/abc');
    expect(init?.method).toBe('PUT');
    const headers = init?.headers as Record<string, string>;
    expect(headers[CSRF_HEADER]).toBe(CSRF_VALUE);
    expect(headers['Content-Type']).toBe('application/json');
    expect(init?.credentials).toBe('same-origin');
    expect(JSON.parse(String(init?.body))).toEqual({ role: 'rtl_433', band_mhz: 915 });
  });

  it('sends the CSRF header on POST and DELETE too', async () => {
    fetchMock.mockImplementation(async () => jsonResponse(200, {}));
    await http.post('/api/config/apply');
    await http.delete('/api/session');
    for (const call of fetchMock.mock.calls) {
      const headers = call[1]?.headers as Record<string, string>;
      expect(headers[CSRF_HEADER]).toBe(CSRF_VALUE);
    }
  });

  it('calls the unauthenticated handler on 401 and throws an ApiError', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: 'unauthenticated' }));
    const handler = vi.fn();
    onUnauthenticated(handler);

    await expect(http.get('/api/health')).rejects.toBeInstanceOf(ApiError);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('does not call the handler for an expected 401 (login form)', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(401, { error: 'invalid_credentials', detail: 'Wrong password' }),
    );
    const handler = vi.fn();
    onUnauthenticated(handler);

    const err = await api.session.login({ username: 'a', password: 'b' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).error).toBe('invalid_credentials');
    expect((err as ApiError).userMessage).toBe('Wrong password');
    expect(handler).not.toHaveBeenCalled();
  });

  it('surfaces {error, detail} from the server', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { error: 'bad_request', detail: 'band_mhz must be 433.92 or 915' }),
    );

    const err = await http.put('/api/radios/x', {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const apiErr = err as ApiError;
    expect(apiErr.status).toBe(400);
    expect(apiErr.error).toBe('bad_request');
    expect(apiErr.detail).toBe('band_mhz must be 433.92 or 915');
    expect(apiErr.userMessage).toBe('band_mhz must be 433.92 or 915');
  });

  it('synthesizes an error when the body is not JSON', async () => {
    fetchMock.mockResolvedValue(new Response('<html>502</html>', { status: 502 }));
    const err = await http.get('/api/health').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).error).toBe('http_502');
    expect((err as ApiError).status).toBe(502);
  });

  it('wraps network failures as status 0', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));
    const err = await http.get('/api/health').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(0);
    expect((err as ApiError).userMessage).toMatch(/Could not reach the gateway/);
  });

  it('encodes station keys in paths and since in readings queries', async () => {
    fetchMock.mockImplementation(async () => jsonResponse(200, []));
    await api.stations.update('Acurite-5n1:0x0C4A:A', { selected: true });
    await api.readings('Fineoffset-WH65B:0x31', new Date('2026-01-02T03:04:05Z'));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/stations/Acurite-5n1%3A0x0C4A%3AA');
    expect(fetchMock.mock.calls[1][0]).toBe(
      '/api/readings?station=Fineoffset-WH65B%3A0x31&since=2026-01-02T03%3A04%3A05.000Z',
    );
  });
});
