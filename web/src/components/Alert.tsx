import type { ReactNode } from 'react';
import type { Tone } from './Stat';

interface AlertProps {
  tone?: Tone;
  title?: ReactNode;
  children?: ReactNode;
  /** Announce changes to screen readers. Use for status that updates in place. */
  live?: boolean;
}

export function Alert({ tone = 'info', title, children, live = false }: AlertProps) {
  const role = tone === 'bad' || tone === 'warn' ? 'alert' : 'status';
  return (
    <div
      className={`alert tone-${tone}`}
      role={live ? role : undefined}
      aria-live={live ? 'polite' : undefined}
    >
      {title && <div className="alert-title">{title}</div>}
      {children && <div className="alert-body">{children}</div>}
    </div>
  );
}
