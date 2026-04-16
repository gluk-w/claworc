import { type InputHTMLAttributes, type Ref, type ReactNode, useId } from 'react';
import { cn } from '@/lib/cn';
import { inputVariants, inputSizeConfig } from './input.variants';

type InputSize = 'sm' | 'md' | 'lg';

interface InputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'ref' | 'size'> {
  ref?: Ref<HTMLInputElement>;
  label?: string;
  error?: string;
  hint?: string;
  /** Icon to display at the start of the input */
  leadingIcon?: ReactNode;
  /** Icon to display at the end of the input */
  trailingIcon?: ReactNode;
  size?: InputSize;
}

export function Input({ ref, label, error, hint, leadingIcon, trailingIcon, size = 'md', className, id, ...props }: InputProps) {
  const generatedId = useId();
  const inputId = id || generatedId;
  const config = inputSizeConfig[size];

  const inputStyles = cn(
    inputVariants({ size }),
    error && 'border-destructive focus-visible:ring-destructive',
    leadingIcon ? config.leadingPadding : config.baseLeftPadding,
    trailingIcon ? config.trailingPadding : config.baseRightPadding
  );

  const iconStyles = cn(
    'absolute top-0 flex items-center justify-center h-full pointer-events-none',
    'text-foreground-muted',
    config.iconWrapper
  );

  return (
    <div className={cn('space-y-1.5', className)}>
      {label && (
        <label htmlFor={inputId} className="text-sm font-medium leading-none">
          {label}
        </label>
      )}

      <div className="relative">
        {leadingIcon && (
          <div className={cn(iconStyles, 'left-0')}>
            {leadingIcon}
          </div>
        )}

        <input
          ref={ref}
          id={inputId}
          className={inputStyles}
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={error ? `${inputId}-error` : hint ? `${inputId}-hint` : undefined}
          {...props}
        />

        {trailingIcon && (
          <div className={cn(iconStyles, 'right-0')}>
            {trailingIcon}
          </div>
        )}
      </div>

      {error && (
        <p id={`${inputId}-error`} className="text-sm text-destructive">
          {error}
        </p>
      )}

      {hint && !error && (
        <p id={`${inputId}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}

export default Input;
