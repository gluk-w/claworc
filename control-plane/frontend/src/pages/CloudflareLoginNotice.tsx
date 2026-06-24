import { ShieldCheck } from "lucide-react";

// Shown on /login when Cloudflare Access (Zero Trust) is the active auth mode.
// There is no username/password form: identity is established by Cloudflare at
// the edge. A user landing here is either signed out of Cloudflare Access or has
// no matching Claworc account, so reloading re-runs the Access challenge.
export default function CloudflareLoginNotice() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="w-full max-w-sm">
        <div className="bg-white rounded-lg shadow-sm border border-gray-200 p-6 text-center">
          <div className="flex justify-center mb-3">
            <ShieldCheck size={32} className="text-blue-600" />
          </div>
          <h1
            data-testid="cf-login-title"
            className="text-xl font-semibold text-gray-900 mb-1"
          >
            Sign in via Cloudflare Access
          </h1>
          <p className="text-sm text-gray-500 mb-6">OpenClaw Orchestrator</p>
          <p className="text-sm text-gray-600 mb-6">
            This deployment authenticates through your organization's Cloudflare
            Access. If you reached this page, your session may have expired or
            your account isn't provisioned yet.
          </p>
          <button
            data-testid="cf-reload-button"
            onClick={() => window.location.reload()}
            className="w-full px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
          >
            Retry sign in
          </button>
          <p className="text-xs text-gray-400 mt-4">
            If the problem persists, contact your administrator to confirm your
            email is registered.
          </p>
        </div>
      </div>
    </div>
  );
}
