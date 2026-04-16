/**
 * Pagination Component (React)
 * Page navigation with prev/next and numbered pages.
 */
import type { HTMLAttributes } from 'react';
import { cn } from '@/lib/cn';
import { paginationItemVariants } from './pagination.variants';

interface PaginationProps extends HTMLAttributes<HTMLElement> {
  /** Current active page (1-indexed) */
  currentPage: number;
  /** Total number of pages */
  totalPages: number;
  /** Callback when a page is selected */
  onPageChange: (page: number) => void;
  /** Maximum number of visible page buttons */
  maxVisible?: number;
  /** Button size variant */
  size?: 'sm' | 'md' | 'lg';
}

/**
 * Build the array of page numbers and ellipsis markers to render.
 *
 * @param current - The 1-indexed active page.
 * @param total   - Total number of pages.
 * @param max     - Size of the central sliding window. When `total <= max`,
 *                  every page is returned directly. Otherwise a window of
 *                  `max` pages is centered around `current`, and first/last
 *                  pages plus `'...'` ellipsis markers are added outside the
 *                  window as needed — so the returned array can contain more
 *                  than `max` entries.
 * @returns An array of page numbers and `'...'` separators.
 */
function getPageRange(current: number, total: number, max: number): (number | '...')[] {
  if (total <= max) {
    return Array.from({ length: total }, (_, i) => i + 1);
  }

  const pages: (number | '...')[] = [];
  const half = Math.floor(max / 2);
  let start = Math.max(1, current - half);
  const end = Math.min(total, start + max - 1);

  if (end - start < max - 1) {
    start = Math.max(1, end - max + 1);
  }

  if (start > 1) {
    pages.push(1);
    if (start > 2) pages.push('...');
  }

  for (let i = start; i <= end; i++) {
    pages.push(i);
  }

  if (end < total) {
    if (end < total - 1) pages.push('...');
    pages.push(total);
  }

  return pages;
}

export function Pagination({
  currentPage,
  totalPages,
  onPageChange,
  maxVisible = 5,
  size = 'md',
  className,
  ...attrs
}: PaginationProps) {
  const pages = getPageRange(currentPage, totalPages, maxVisible);
  const hasPrev = currentPage > 1;
  const hasNext = currentPage < totalPages;

  return (
    <nav className={cn('flex items-center gap-1', className)} aria-label="Pagination" {...attrs}>
      {/* Previous */}
      {hasPrev ? (
        <button
          type="button"
          className={cn(paginationItemVariants({ variant: 'default', size }))}
          aria-label="Previous page"
          onClick={() => onPageChange(currentPage - 1)}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M15 18l-6-6 6-6" />
          </svg>
        </button>
      ) : (
        <span
          className={cn(paginationItemVariants({ variant: 'disabled', size }))}
          aria-disabled="true"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M15 18l-6-6 6-6" />
          </svg>
        </span>
      )}

      {/* Page Numbers */}
      {pages.map((page, index) =>
        page === '...' ? (
          <span
            key={`ellipsis-${index}`}
            className={cn(
              paginationItemVariants({ variant: 'default', size }),
              'cursor-default hover:bg-transparent',
            )}
            role="separator"
            aria-label="More pages"
          >
            <span aria-hidden="true">...</span>
          </span>
        ) : page === currentPage ? (
          <span
            key={page}
            className={cn(paginationItemVariants({ variant: 'active', size }))}
            aria-current="page"
          >
            {page}
          </span>
        ) : (
          <button
            key={page}
            type="button"
            className={cn(paginationItemVariants({ variant: 'default', size }))}
            onClick={() => onPageChange(page)}
          >
            {page}
          </button>
        ),
      )}

      {/* Next */}
      {hasNext ? (
        <button
          type="button"
          className={cn(paginationItemVariants({ variant: 'default', size }))}
          aria-label="Next page"
          onClick={() => onPageChange(currentPage + 1)}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M9 18l6-6-6-6" />
          </svg>
        </button>
      ) : (
        <span
          className={cn(paginationItemVariants({ variant: 'disabled', size }))}
          aria-disabled="true"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M9 18l6-6-6-6" />
          </svg>
        </span>
      )}
    </nav>
  );
}

export default Pagination;
