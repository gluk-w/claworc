import { type SelectHTMLAttributes, type Ref, useId } from 'react';
import { cn } from '@/lib/cn';
import { selectVariants } from './select.variants';

type SelectSize = 'sm' | 'md' | 'lg';

interface Option {
  value: string;
  label: string;
  disabled?: boolean;
}

interface SelectProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'ref' | 'size'> {
  ref?: Ref<HTMLSelectElement>;
  label?: string;
  error?: string;
  hint?: string;
  size?: SelectSize;
  options: Option[];
  placeholder?: string;
}

export function Select({ ref, label, error, hint, size = 'md', options, placeholder, className, id, ...props }: SelectProps) {
  const generatedId = useId();
  const selectId = id || generatedId;

  const selectStyles = cn(
    selectVariants({ size }),
    error && 'border-destructive focus-visible:ring-destructive'
  );

  return (
    <div className={cn('space-y-1.5', className)}>
      {label && (
        <label htmlFor={selectId} className="text-sm font-medium leading-none">
          {label}
        </label>
      )}

      <div className="relative">
        <select
          ref={ref}
          id={selectId}
          className={selectStyles}
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={error ? `${selectId}-error` : hint ? `${selectId}-hint` : undefined}
          {...props}
        >
          {placeholder && (
            <option value="" disabled>
              {placeholder}
            </option>
          )}
          {options.map((option) => (
            <option key={option.value} value={option.value} disabled={option.disabled}>
              {option.label}
            </option>
          ))}
        </select>

        <div className="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-3">
          <svg
            className="h-4 w-4 text-muted-foreground"
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 20 20"
            fill="currentColor"
            aria-hidden="true"
          >
            <path
              fillRule="evenodd"
              d="M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z"
              clipRule="evenodd"
            />
          </svg>
        </div>
      </div>

      {error && (
        <p id={`${selectId}-error`} className="text-sm text-destructive">
          {error}
        </p>
      )}

      {hint && !error && (
        <p id={`${selectId}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}

export default Select;
