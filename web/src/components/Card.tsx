import type { ReactNode } from 'react';

interface CardProps {
  title?: ReactNode;
  /** Right-aligned content in the header, for example a Badge or a button. */
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  /** Allows the page to reference the card heading for aria-labelledby. */
  id?: string;
}

export function Card({ title, actions, children, className, id }: CardProps) {
  const headingId = id ? `${id}-title` : undefined;
  return (
    <section className={`card${className ? ` ${className}` : ''}`} aria-labelledby={headingId}>
      {(title || actions) && (
        <header className="card-header">
          {title ? (
            <h2 className="card-title" id={headingId}>
              {title}
            </h2>
          ) : (
            <span />
          )}
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      <div className="card-body">{children}</div>
    </section>
  );
}
