import { type InputHTMLAttributes, type Ref, useId } from 'react';
import { cn } from '@/lib/cn';

interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'ref'> {
  ref?: Ref<HTMLInputElement>;
  label?: string;
  description?: string;
  error?: string;
}

export function Checkbox({ ref, label, description, error, className, id, ...props }: CheckboxProps) {
  const generatedId = useId();
  const checkboxId = id || generatedId;

  return (
    <div className={cn('group', className)}>
      <label
        htmlFor={checkboxId}
        className={cn(
          'relative flex items-start gap-3 cursor-pointer',
          'select-none',
          props.disabled && 'cursor-not-allowed opacity-50'
        )}
      >
        {/* Custom checkbox visual */}
        <div className="relative flex items-center justify-center mt-0.5">
          <input
            ref={ref}
            type="checkbox"
            id={checkboxId}
            className="peer sr-only"
            aria-invalid={error ? 'true' : undefined}
            aria-describedby={description ? `${checkboxId}-description` : undefined}
            {...props}
          />
          {/* Checkbox box */}
          <div
            className={cn(
              'h-[18px] w-[18px] shrink-0 rounded-[5px]',
              'border-2 border-border',
              'bg-background',
              'transition-all duration-150 ease-out',
              'peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background',
              'peer-checked:bg-foreground peer-checked:border-foreground',
              'peer-hover:border-foreground-muted',
              'peer-checked:peer-hover:bg-foreground/90',
              error && 'border-destructive peer-focus-visible:ring-destructive'
            )}
          />
          {/* Checkmark icon */}
          <svg
            className={cn(
              'absolute h-3 w-3 text-background',
              'opacity-0 scale-50',
              'transition-all duration-150 ease-out',
              'peer-checked:opacity-100 peer-checked:scale-100'
            )}
            viewBox="0 0 12 12"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M2.5 6L5 8.5L9.5 3.5" />
          </svg>
        </div>

        {/* Label and description */}
        {(label || description) && (
          <div className="grid gap-0.5 leading-tight">
            {label && (
              <span className="text-sm font-medium text-foreground">
                {label}
              </span>
            )}
            {description && (
              <span
                id={`${checkboxId}-description`}
                className="text-xs text-foreground-subtle leading-normal"
              >
                {description}
              </span>
            )}
          </div>
        )}
      </label>

      {error && (
        <p className="mt-2 text-sm text-destructive pl-[30px]">{error}</p>
      )}
    </div>
  );
}

export default Checkbox;
