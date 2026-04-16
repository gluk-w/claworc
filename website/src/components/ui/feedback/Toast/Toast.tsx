import { useState, useCallback, useEffect, createContext, useContext, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { toastVariants, toastIconColors } from './toast.variants';
import { Icon } from '@/components/ui/primitives/Icon/Icon';

type ToastVariant = 'default' | 'success' | 'error' | 'warning' | 'info';

interface Toast {
  id: string;
  title?: string;
  description?: string;
  variant?: ToastVariant;
  duration?: number;
}

interface ToastContextValue {
  toast: (options: Omit<Toast, 'id'>) => void;
  dismiss: (id: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

export function useToast() {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error('useToast must be used within a ToastProvider');
  }
  return context;
}

let toastCount = 0;

const icons: Record<ToastVariant, ReactNode> = {
  default: null,
  success: <Icon name="check-circle" size="md" />,
  error:   <Icon name="x-circle"     size="md" />,
  warning: <Icon name="alert-triangle" size="md" />,
  info:    <Icon name="info"          size="md" />,
};

function ToastItem({ toast, onDismiss }: { toast: Toast; onDismiss: (id: string) => void }) {
  const [isExiting, setIsExiting] = useState(false);
  const variant = toast.variant || 'default';

  useEffect(() => {
    const duration = toast.duration ?? 5000;
    if (duration === Infinity) return;

    const timer = setTimeout(() => {
      setIsExiting(true);
      setTimeout(() => onDismiss(toast.id), 300);
    }, duration);

    return () => clearTimeout(timer);
  }, [toast.id, toast.duration, onDismiss]);

  const handleDismiss = () => {
    setIsExiting(true);
    setTimeout(() => onDismiss(toast.id), 300);
  };

  return (
    <div
      className={cn(
        toastVariants({ variant }),
        'pointer-events-auto',
        isExiting ? 'opacity-0 translate-x-full transition-all duration-300' : 'animate-toast-in'
      )}
      role="alert"
    >
      {icons[variant] && (
        <div className={cn('mt-0.5', toastIconColors[variant])}>
          {icons[variant]}
        </div>
      )}
      <div className="flex-1 min-w-0">
        {toast.title && (
          <p className="text-sm font-semibold">{toast.title}</p>
        )}
        {toast.description && (
          <p className="text-sm text-foreground-muted mt-0.5">{toast.description}</p>
        )}
      </div>
      <button
        type="button"
        className="shrink-0 p-1 -mr-1 -mt-1 rounded-md text-foreground-muted hover:text-foreground transition-colors"
        onClick={handleDismiss}
        aria-label="Dismiss"
      >
        <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="18" y1="6" x2="6" y2="18" />
          <line x1="6" y1="6" x2="18" y2="18" />
        </svg>
      </button>
    </div>
  );
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const toast = useCallback((options: Omit<Toast, 'id'>) => {
    const id = `toast-${++toastCount}`;
    setToasts((prev) => [...prev, { ...options, id }]);
  }, []);

  const dismiss = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  return (
    <ToastContext.Provider value={{ toast, dismiss }}>
      {children}
      {/* Toast container */}
      <div
        className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2 max-w-[420px] w-full pointer-events-none"
        aria-live="polite"
        aria-label="Notifications"
      >
        {toasts.map((t) => (
          <ToastItem key={t.id} toast={t} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export default ToastProvider;
