// Thin fetch wrapper for the gateway API.
//
// - Sends the X-Requested-With: pwsgw header the server requires on every
//   mutating request (it is harmless on GET, so we send it always).
// - Turns non-2xx responses into ApiError carrying the server's {error, detail}.
// - On 401 with error "unauthenticated" it notifies the registered handler so
//   the app can drop its session state and route to the login page.

import type { ApiErrorBody } from './types';

export const CSRF_HEADER = 'X-Requested-With';
export const CSRF_VALUE = 'pwsgw';

export class ApiError extends Error {
  readonly status: number;
  readonly error: string;
  readonly detail: string | undefined;

  constructor(status: number, body: ApiErrorBody) {
    super(body.detail ? `${body.error}: ${body.detail}` : body.error);
    this.name = 'ApiError';
    this.status = status;
    this.error = body.error;
    this.detail = body.detail;
  }

  /** Short text suitable for showing to a person. */
  get userMessage(): string {
    if (this.detail) return this.detail;
    return describeError(this.error, this.status);
  }
}

const KNOWN_ERRORS: Record<string, string> = {
  unauthenticated: 'You are signed out. Please sign in again.',
  invalid_credentials: 'Username or password is wrong.',
  csrf: 'The request was rejected as a possible cross-site request. Reload the page and try again.',
  not_found: 'That item no longer exists.',
  rate_limited: 'Too many attempts. Wait a moment and try again.',
  weak_password: 'Choose a longer password.',
};

export function describeError(code: string, status: number): string {
  if (code in KNOWN_ERRORS) return KNOWN_ERRORS[code];
  if (status >= 500) return 'The gateway reported an internal problem. Check the logs page.';
  if (status === 0)
    return 'Could not reach the gateway. Check that the Pi is on and on the same network.';
  return code.replace(/_/g, ' ');
}

type UnauthenticatedHandler = () => void;
let unauthenticatedHandler: UnauthenticatedHandler | null = null;

/** Register the callback invoked when any request returns 401 unauthenticated. */
export function onUnauthenticated(handler: UnauthenticatedHandler | null): void {
  unauthenticatedHandler = handler;
}

export interface RequestOptions {
  /**
   * Set when a 401 is an expected outcome for this request (the login form, the
   * session probe at boot) so it does not trigger the global sign-out handler.
   */
  allowUnauthenticated?: boolean;
  signal?: AbortSignal;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: RequestOptions = {},
  accept: 'json' | 'text' = 'json',
): Promise<T> {
  const headers: Record<string, string> = {
    [CSRF_HEADER]: CSRF_VALUE,
    Accept: accept === 'json' ? 'application/json' : 'text/plain',
  };
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: opts.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err;
    throw new ApiError(0, { error: 'network', detail: describeError('network', 0) });
  }

  if (!res.ok) {
    const errBody = await parseErrorBody(res);
    if (res.status === 401 && errBody.error === 'unauthenticated' && !opts.allowUnauthenticated) {
      unauthenticatedHandler?.();
    }
    throw new ApiError(res.status, errBody);
  }

  if (res.status === 204) return undefined as T;
  if (accept === 'text') return (await res.text()) as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : null) as T;
}

async function parseErrorBody(res: Response): Promise<ApiErrorBody> {
  const text = await res.text().catch(() => '');
  try {
    const parsed: unknown = JSON.parse(text);
    if (
      parsed &&
      typeof parsed === 'object' &&
      typeof (parsed as ApiErrorBody).error === 'string'
    ) {
      return parsed as ApiErrorBody;
    }
  } catch {
    // fall through to a synthesized body
  }
  return { error: `http_${res.status}`, detail: text.slice(0, 200) || undefined };
}

export const http = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, undefined, opts),
  getText: (path: string, opts?: RequestOptions) =>
    request<string>('GET', path, undefined, opts, 'text'),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>('POST', path, body ?? {}, opts),
  put: <T>(path: string, body: unknown, opts?: RequestOptions) =>
    request<T>('PUT', path, body, opts),
  delete: <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, undefined, opts),
};

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

export function errorMessage(err: unknown): string {
  if (isApiError(err)) return err.userMessage;
  if (err instanceof Error) return err.message;
  return 'Something went wrong.';
}
