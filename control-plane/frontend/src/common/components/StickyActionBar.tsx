import type { ReactNode } from "react";

interface StickyActionBarProps {
  visible: boolean;
  children: ReactNode;
}

export default function StickyActionBar({ visible, children }: StickyActionBarProps) {
  return (
    <div
      aria-hidden={!visible}
      className={`fixed bottom-0 left-16 right-0 z-30 bg-white border-t border-gray-200 shadow-[0_-4px_12px_-6px_rgba(0,0,0,0.08)] transform transition-transform duration-200 ease-out ${
        visible ? "translate-y-0" : "translate-y-full pointer-events-none"
      }`}
    >
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="max-w-2xl mx-auto py-3 flex justify-end gap-3">
          {children}
        </div>
      </div>
    </div>
  );
}
