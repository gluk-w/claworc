import { type InputHTMLAttributes, type Ref, useId } from 'react';
import { cn } from '@/lib/cn';

interface RadioProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'ref'> {
  ref?: Ref<HTMLInputElement>;
  label?: string;
  description?: string;
  error?: string;
  /** Use card-style selection UI */
  variant?: 'default' | 'card';
}

export function Radio({ ref, label, description, error, variant = 'default', className, id, ...props }: RadioProps) {
  const generatedId = useId();
  const radioId = id || generatedId;

  if (variant === 'card') {
    return (
      <div className={cn('group', className)}>
        <label
          htmlFor={radioId}
          className={cn(
            'relative flex items-start gap-4 p-4 cursor-pointer',
            'rounded-xl border-2 border-border',
            'bg-background',
            'transition-all duration-150 ease-out',
            'hover:border-foreground-subtle hover:bg-secondary/30',
            'has-[:checked]:border-foreground has-[:checked]:bg-secondary/50',
            'has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring has-[:focus-visible]:ring-offset-2',
            props.disabled && 'cursor-not-allowed opacity-50',
            error && 'border-destructive'
          )}
        >
          {/* Radio indicator with checkmark inside */}
          <div className="relative flex items-center justify-center mt-0.5 shrink-0">
            <input
              ref={ref}
              type="radio"
              id={radioId}
              className="peer sr-only"
              aria-invalid={error ? 'true' : undefined}
              aria-describedby={description ? `${radioId}-description` : undefined}
              {...props}
            />
            {/* Outer circle */}
            <div
              className={cn(
                'h-5 w-5 rounded-full',
                'border-2 border-border',
                'bg-background',
                'transition-all duration-150 ease-out',
                'peer-checked:border-foreground peer-checked:bg-foreground',
                'group-hover:border-foreground-muted'
              )}
            />
            {/* Checkmark inside circle */}
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
              strokeWidth="2.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <path d="M2.5 6L5 8.5L9.5 3.5" />
            </svg>
          </div>

          {/* Content */}
          {(label || description) && (
            <div className="flex-1 grid gap-0.5">
              {label && (
                <span className="text-sm font-semibold text-foreground">
                  {label}
                </span>
              )}
              {description && (
                <span
                  id={`${radioId}-description`}
                  className="text-xs text-foreground-subtle leading-normal"
                >
                  {description}
                </span>
              )}
            </div>
          )}
        </label>

        {error && (
          <p className="mt-2 text-sm text-destructive">{error}</p>
        )}
      </div>
    );
  }

  // Default radio style
  return (
    <div className={cn('group', className)}>
      <label
        htmlFor={radioId}
        className={cn(
          'relative flex items-start gap-3 cursor-pointer',
          'select-none',
          props.disabled && 'cursor-not-allowed opacity-50'
        )}
      >
        {/* Custom radio visual */}
        <div className="relative flex items-center justify-center mt-0.5 shrink-0">
          <input
            ref={ref}
            type="radio"
            id={radioId}
            className="peer sr-only"
            aria-invalid={error ? 'true' : undefined}
            aria-describedby={description ? `${radioId}-description` : undefined}
            {...props}
          />
          {/* Radio circle */}
          <div
            className={cn(
              'h-[18px] w-[18px] rounded-full',
              'border-2 border-border',
              'bg-background',
              'transition-all duration-150 ease-out',
              'peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background',
              'peer-checked:border-foreground',
              'group-hover:border-foreground-muted',
              error && 'border-destructive peer-focus-visible:ring-destructive'
            )}
          />
          {/* Inner dot */}
          <div
            className={cn(
              'absolute h-2 w-2 rounded-full',
              'bg-foreground',
              'opacity-0 scale-0',
              'transition-all duration-150 ease-out',
              'peer-checked:opacity-100 peer-checked:scale-100'
            )}
          />
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
                id={`${radioId}-description`}
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

export default Radio;
