import { useCallback, useEffect, useRef, useState } from 'react';

export interface PollState<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  /** Time of the last successful fetch, ms since epoch. */
  updatedAt: number | null;
  refresh: () => Promise<void>;
}

/**
 * Fetch `fn` now and every `intervalMs`, pausing while the tab is hidden. The
 * loader receives an AbortSignal that fires on unmount or when a new request
 * supersedes it. Pass `enabled: false` to stop polling (for example after the
 * session is lost).
 */
export function usePoll<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  intervalMs: number,
  deps: unknown[] = [],
  enabled = true,
): PollState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const run = useCallback(async () => {
    abortRef.current?.abort();
    const ac = new AbortController();
    abortRef.current = ac;
    try {
      const result = await fnRef.current(ac.signal);
      if (ac.signal.aborted) return;
      setData(result);
      setError(null);
      setUpdatedAt(Date.now());
    } catch (err) {
      if (ac.signal.aborted) return;
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      if (!ac.signal.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!enabled) return;
    setLoading(true);
    void run();
    let timer: ReturnType<typeof setInterval> | null = null;

    const start = () => {
      if (timer === null) timer = setInterval(() => void run(), intervalMs);
    };
    const stop = () => {
      if (timer !== null) {
        clearInterval(timer);
        timer = null;
      }
    };
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') stop();
      else {
        void run();
        start();
      }
    };

    if (document.visibilityState !== 'hidden') start();
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', onVisibility);
      abortRef.current?.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, intervalMs, enabled, ...deps]);

  return { data, error, loading, updatedAt, refresh: run };
}
