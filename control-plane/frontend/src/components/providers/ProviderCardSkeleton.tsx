export default function ProviderCardSkeleton() {
  return (
    <div
      data-testid="provider-card-skeleton"
      className="bg-white rounded-lg border border-gray-200 p-4 flex flex-col gap-3 animate-pulse"
      aria-hidden="true"
    >
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <div className="h-6 w-6 bg-gray-200 rounded-md" />
          <div className="h-4 w-20 bg-gray-200 rounded" />
        </div>
        <div className="h-4 w-4 bg-gray-200 rounded" />
      </div>

      {/* Description */}
      <div className="space-y-1.5">
        <div className="h-3 w-full bg-gray-200 rounded" />
        <div className="h-3 w-2/3 bg-gray-200 rounded" />
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between mt-auto pt-2 border-t border-gray-100">
        <div className="h-4 w-16 bg-gray-200 rounded" />
        <div className="h-6 w-16 bg-gray-200 rounded-md" />
      </div>
    </div>
  );
}
