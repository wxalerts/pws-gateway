import type { ReactNode } from 'react';
import type { Tone } from './Stat';

interface BadgeProps {
  tone?: Tone;
  children: ReactNode;
  title?: string;
}

export function Badge({ tone = 'neutral', children, title }: BadgeProps) {
  return (
    <span className={`badge tone-${tone}`} title={title}>
      {children}
    </span>
  );
}
