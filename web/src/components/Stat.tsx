import type { ReactNode } from 'react';

export type Tone = 'neutral' | 'ok' | 'warn' | 'bad' | 'info';

interface StatProps {
  label: string;
  value: ReactNode;
  /** Secondary line under the value. */
  hint?: ReactNode;
  tone?: Tone;
}

/** A labelled number for dashboards. Tone colours the value only. */
export function Stat({ label, value, hint, tone = 'neutral' }: StatProps) {
  return (
    <div className={`stat tone-${tone}`}>
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      {hint !== undefined && <div className="stat-hint">{hint}</div>}
    </div>
  );
}

export function StatGrid({ children }: { children: ReactNode }) {
  return <div className="stat-grid">{children}</div>;
}
