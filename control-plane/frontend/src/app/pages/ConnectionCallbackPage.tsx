import { useEffect } from "react";

/**
 * Landing page Composio redirects to after the user authorizes a connection.
 * It runs entirely in the browser: it notifies the opener window (the Add
 * connection wizard) and closes itself. No backend involvement — the wizard
 * then confirms the connection status via the API.
 */
export default function ConnectionCallbackPage() {
  useEffect(() => {
    try {
      window.opener?.postMessage(
        { type: "composio-connected" },
        window.location.origin,
      );
    } catch {
      /* opener may be gone — ignore */
    }
    window.close();
  }, []);

  return (
    <div className="flex min-h-screen items-center justify-center text-sm text-gray-500">
      Connection authorized. You can close this window.
    </div>
  );
}
