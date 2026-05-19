import type { ReactNode } from "react";

export type PageTab = { key: string; label: string };

type Props = {
  title: ReactNode;
  actions?: ReactNode;
  banner?: ReactNode;
  tabs?: PageTab[];
  activeTab?: string;
  onTabChange?: (key: string) => void;
  width?: "narrow" | "full";
  stickyBarSpace?: boolean;
  children: ReactNode;
};

export default function Page({
  title,
  actions,
  banner,
  tabs,
  activeTab,
  onTabChange,
  width = "full",
  stickyBarSpace = false,
  children,
}: Props) {
  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">{title}</h1>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>

      {banner}

      {tabs && (
        <div className="flex border-b border-gray-200 mb-6">
          {tabs.map((t) => (
            <button
              key={t.key}
              type="button"
              onClick={() => onTabChange?.(t.key)}
              className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
                activeTab === t.key
                  ? "border-blue-600 text-blue-600"
                  : "border-transparent text-gray-500 hover:text-gray-700"
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      )}

      <div
        className={[
          width === "narrow" ? "max-w-2xl" : "",
          stickyBarSpace ? "pb-24" : "",
        ]
          .filter(Boolean)
          .join(" ")}
      >
        {children}
      </div>
    </div>
  );
}
