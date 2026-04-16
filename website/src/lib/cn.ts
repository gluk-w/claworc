import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * Merge Tailwind CSS classes with proper precedence handling
 *
 * Combines clsx for conditional classes with tailwind-merge
 * to properly handle Tailwind class conflicts.
 *
 * @example
 * cn('px-2 py-1', isActive && 'bg-primary', className)
 * cn('text-sm', { 'font-bold': isBold }, customClasses)
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
