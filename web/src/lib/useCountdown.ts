import { useEffect, useState } from 'react';

/** Whole seconds left until `iso`, updated every second. 0 once it has passed. */
export function useCountdown(iso: string | null): number {
  const target = iso ? Date.parse(iso) : NaN;
  const left = () =>
    Number.isNaN(target) ? 0 : Math.max(0, Math.ceil((target - Date.now()) / 1000));
  const [secs, setSecs] = useState(left);
  useEffect(() => {
    setSecs(left());
    if (Number.isNaN(target)) return;
    const t = setInterval(() => setSecs(left()), 1000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);
  return secs;
}

/** 9:05 style clock for a number of seconds. */
export function formatClock(secs: number): string {
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}
