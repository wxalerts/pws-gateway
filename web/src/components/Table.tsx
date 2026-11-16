import type { ReactNode } from 'react';

export interface Column<T> {
  key: string;
  header: ReactNode;
  render: (row: T) => ReactNode;
  /** Right-align numbers. */
  numeric?: boolean;
  /** Hide on narrow screens. */
  optional?: boolean;
}

interface TableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  caption?: string;
  empty?: ReactNode;
  /** Returns extra class names for a row, for example to highlight the selected one. */
  rowClass?: (row: T) => string | undefined;
}

export function Table<T>({ columns, rows, rowKey, caption, empty, rowClass }: TableProps<T>) {
  return (
    <div className="table-wrap">
      <table className="table">
        {caption && <caption className="visually-hidden">{caption}</caption>}
        <thead>
          <tr>
            {columns.map((c) => (
              <th
                key={c.key}
                scope="col"
                className={
                  [c.numeric ? 'num' : '', c.optional ? 'optional' : ''].join(' ').trim() ||
                  undefined
                }
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td colSpan={columns.length} className="table-empty">
                {empty ?? 'Nothing to show yet.'}
              </td>
            </tr>
          )}
          {rows.map((row) => (
            <tr key={rowKey(row)} className={rowClass?.(row)}>
              {columns.map((c) => (
                <td
                  key={c.key}
                  className={
                    [c.numeric ? 'num' : '', c.optional ? 'optional' : ''].join(' ').trim() ||
                    undefined
                  }
                >
                  {c.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
