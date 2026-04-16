import { type TextareaHTMLAttributes, type Ref, useId } from 'react';
import { cn } from '@/lib/cn';
import { textareaVariants } from './textarea.variants';

type TextareaSize = 'sm' | 'md' | 'lg';

interface TextareaProps extends Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'ref'> {
  ref?: Ref<HTMLTextAreaElement>;
  label?: string;
  error?: string;
  hint?: string;
  size?: TextareaSize;
}

export function Textarea({ ref, label, error, hint, size = 'md', className, id, rows = 4, ...props }: TextareaProps) {
  const generatedId = useId();
  const textareaId = id || generatedId;

  const textareaStyles = cn(
    textareaVariants({ size }),
    error && 'border-destructive focus-visible:ring-destructive'
  );

  return (
    <div className={cn('space-y-1.5', className)}>
      {label && (
        <label htmlFor={textareaId} className="text-sm font-medium leading-none">
          {label}
        </label>
      )}

      <textarea
        ref={ref}
        id={textareaId}
        className={textareaStyles}
        rows={rows}
        aria-invalid={error ? 'true' : undefined}
        aria-describedby={error ? `${textareaId}-error` : hint ? `${textareaId}-hint` : undefined}
        {...props}
      />

      {error && (
        <p id={`${textareaId}-error`} className="text-sm text-destructive">
          {error}
        </p>
      )}

      {hint && !error && (
        <p id={`${textareaId}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}

export default Textarea;
