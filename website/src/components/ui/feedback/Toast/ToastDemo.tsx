import { ToastProvider, useToast } from './Toast';

function ToastButtons() {
  const { toast } = useToast();

  return (
    <div className="flex flex-wrap gap-3">
      <button
        type="button"
        className="inline-flex items-center justify-center gap-2 rounded-md border border-border bg-background px-4 py-2 text-sm font-medium text-foreground transition-colors hover:bg-secondary"
        onClick={() =>
          toast({
            title: 'Default toast',
            description: 'This is a default notification.',
          })
        }
      >
        Default
      </button>
      <button
        type="button"
        className="inline-flex items-center justify-center gap-2 rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-green-700"
        onClick={() =>
          toast({
            variant: 'success',
            title: 'Success!',
            description: 'Your changes have been saved.',
          })
        }
      >
        Success
      </button>
      <button
        type="button"
        className="inline-flex items-center justify-center gap-2 rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-700"
        onClick={() =>
          toast({
            variant: 'error',
            title: 'Error',
            description: 'Something went wrong. Please try again.',
          })
        }
      >
        Error
      </button>
      <button
        type="button"
        className="inline-flex items-center justify-center gap-2 rounded-md bg-amber-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-amber-600"
        onClick={() =>
          toast({
            variant: 'warning',
            title: 'Warning',
            description: 'Your session will expire in 5 minutes.',
          })
        }
      >
        Warning
      </button>
      <button
        type="button"
        className="inline-flex items-center justify-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
        onClick={() =>
          toast({
            variant: 'info',
            title: 'Info',
            description: 'A new version is available for download.',
          })
        }
      >
        Info
      </button>
    </div>
  );
}

export function ToastDemo() {
  return (
    <ToastProvider>
      <ToastButtons />
    </ToastProvider>
  );
}
