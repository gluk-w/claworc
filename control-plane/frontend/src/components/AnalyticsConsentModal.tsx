import { useEffect } from "react";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";

// AnalyticsConsentModal renders once per installation, the first time an
// authenticated user lands on the dashboard. After the user picks a choice,
// `analytics_consent` is no longer "unset" and this component returns null.
export default function AnalyticsConsentModal() {
  const { data: settings } = useSettings();
  const updateMutation = useUpdateSettings();

  const isUnset = settings?.analytics_consent === "unset";

  useEffect(() => {
    if (!isUnset) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        updateMutation.mutate({ analytics_consent: "opt_out" });
      } else if (e.key === "Enter") {
        updateMutation.mutate({ analytics_consent: "opt_in" });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [isUnset, updateMutation]);

  if (!isUnset) return null;

  const decide = (consent: "opt_in" | "opt_out") => {
    updateMutation.mutate({ analytics_consent: consent });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/50" />
      <div className="relative bg-white rounded-lg shadow-lg p-6 max-w-md w-full mx-4">
        <h3 className="text-lg font-semibold text-gray-900 mb-2">
          Help improve Claworc
        </h3>
        <p className="text-sm text-gray-600 mb-3">
          We'd like to collect <strong>anonymous</strong> usage statistics. We
          never collect API keys, env-var values, file paths, prompts, or
          instance names. You can change your choice anytime in{" "}
          <span className="font-medium">Settings → Anonymous Analytics</span>.
        </p>
        <p className="text-sm text-gray-600 mb-5">
          <a
            href="https://claworc.com/docs/analytics"
            target="_blank"
            rel="noreferrer"
            className="text-blue-600 hover:underline"
          >
            Read more
          </a>
          .
        </p>
        <div className="flex items-center justify-end gap-3">
          <button
            onClick={() => decide("opt_out")}
            disabled={updateMutation.isPending}
            className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50"
          >
            No thanks
          </button>
          <button
            onClick={() => decide("opt_in")}
            disabled={updateMutation.isPending}
            className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            Share analytics
          </button>
        </div>
      </div>
    </div>
  );
}
