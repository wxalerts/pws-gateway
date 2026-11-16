// Small formatting helpers for times, durations and sizes.

export function formatDuration(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 0) return '?';
  const s = Math.floor(totalSeconds);
  const d = Math.floor(s / 86_400);
  const h = Math.floor((s % 86_400) / 3_600);
  const m = Math.floor((s % 3_600) / 60);
  const sec = s % 60;
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${sec}s`;
  return `${sec}s`;
}

/** "3 minutes ago", "just now", "in 2 minutes". */
export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return 'never';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const diff = Math.round((now - t) / 1000);
  const abs = Math.abs(diff);
  const suffix = diff >= 0 ? 'ago' : 'from now';
  if (abs < 5) return 'just now';
  if (abs < 60) return `${abs}s ${suffix}`;
  if (abs < 3_600) return `${Math.floor(abs / 60)}m ${suffix}`;
  if (abs < 86_400) return `${Math.floor(abs / 3_600)}h ${suffix}`;
  return `${Math.floor(abs / 86_400)}d ${suffix}`;
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return 'never';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return new Date(t).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

export function formatMb(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${Math.round(mb)} MB`;
}

export function formatPercent(pct: number): string {
  return `${Math.round(pct)}%`;
}

export function formatBand(mhz: number): string {
  return `${mhz} MHz`;
}
