/**
 * Table Component (React)
 * A styled data table with hover rows and responsive design.
 */
import type { HTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

interface Column {
  key: string;
  label: string;
  align?: 'left' | 'center' | 'right';
  class?: string;
}

interface TableProps extends HTMLAttributes<HTMLDivElement> {
  columns: Column[];
  rows: Record<string, unknown>[];
  /** Enable row hover highlighting */
  hoverable?: boolean;
  /** Enable row striping */
  striped?: boolean;
  /** Make table compact */
  compact?: boolean;
}

const alignClasses = {
  left: 'text-left',
  center: 'text-center',
  right: 'text-right',
};

export function Table({
  columns,
  rows,
  hoverable = true,
  striped = false,
  compact = false,
  className,
  ...attrs
}: TableProps) {
  const cellPadding = compact ? 'px-3 py-2' : 'px-4 py-3';

  return (
    <div
      className={cn('overflow-auto rounded-lg border border-border', className)}
      {...attrs}
    >
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border bg-secondary/50">
            {columns.map((col) => (
              <th
                key={col.key}
                className={cn(
                  cellPadding,
                  'font-medium text-foreground-muted',
                  alignClasses[col.align || 'left'],
                  col.class,
                )}
              >
                {col.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr
              key={i}
              className={cn(
                'border-b border-border last:border-b-0',
                'transition-colors',
                hoverable && 'hover:bg-secondary/30',
                striped && i % 2 === 1 && 'bg-secondary/20',
              )}
            >
              {columns.map((col) => (
                <td
                  key={col.key}
                  className={cn(
                    cellPadding,
                    'text-foreground',
                    alignClasses[col.align || 'left'],
                    col.class,
                  )}
                >
                  {String(row[col.key] ?? '')}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default Table;
