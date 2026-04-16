import { type HTMLAttributes, type Ref, type ReactNode, useState } from 'react';
import { cn } from '@/lib/cn';
import { alertVariants, alertIconColors, alertAccentColors, type AlertVariants } from './alert.variants';

interface AlertProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref' | 'title'> {
  ref?: Ref<HTMLDivElement>;
  variant?: NonNullable<AlertVariants['variant']>;
  title?: string;
  dismissible?: boolean;
  onDismiss?: () => void;
  children?: ReactNode;
}

const iconPaths: Record<string, string> = {
  info: 'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zM12 16v-4M12 8h.01',
  success: 'M22 11.08V12a10 10 0 1 1-5.93-9.14M22 4L12 14.01l-3-3',
  warning: 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01',
  error: 'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zM15 9l-6 6M9 9l6 6',
};

export function Alert({ ref, variant = 'info', title, dismissible = false, onDismiss, className, children, ...rest }: AlertProps) {
  const [dismissed, setDismissed] = useState(false);
  const [hiding, setHiding] = useState(false);

  if (dismissed) return null;

  const handleDismiss = () => {
    setHiding(true);
    setTimeout(() => {
      setDismissed(true);
      onDismiss?.();
    }, 150);
  };

  return (
    <div
      ref={ref}
      className={cn(
        alertVariants({ variant }),
        hiding && 'opacity-0 scale-[0.98]',
        className
      )}
      style={{ transition: 'opacity 150ms ease-out, transform 150ms ease-out' }}
      role="alert"
      {...rest}
    >
      <div className={cn('absolute left-0 top-0 bottom-0 w-1', alertAccentColors[variant])} />

      <div className={cn('shrink-0 mt-0.5', alertIconColors[variant])}>
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="w-5 h-5" aria-hidden="true">
          {iconPaths[variant].split('M').filter(Boolean).map((d, i) => (
            <path key={i} d={`M${d}`} />
          ))}
        </svg>
      </div>

      <div className="flex-1 min-w-0 pl-1">
        {title && (
          <h5 className="font-semibold text-sm mb-1 text-foreground">{title}</h5>
        )}
        <div className="text-sm leading-relaxed text-foreground-muted">{children}</div>
      </div>

      {dismissible && (
        <button
          type="button"
          className="shrink-0 p-1 -mr-1 -mt-1 rounded-md text-foreground-muted hover:text-foreground hover:bg-secondary transition-colors"
          aria-label="Dismiss"
          onClick={handleDismiss}
        >
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="w-4 h-4" aria-hidden="true">
            <path d="M18 6L6 18" /><path d="M6 6l12 12" />
          </svg>
        </button>
      )}
    </div>
  );
}

export default Alert;
