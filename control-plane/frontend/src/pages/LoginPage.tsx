import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { Fingerprint } from "lucide-react";
import { isAxiosError } from "axios";
import { useAuth } from "@/contexts/AuthContext";
import {
  webAuthnLoginBegin,
  webAuthnLoginFinish,
} from "@/api/auth";
import { startAuthentication } from "@simplewebauthn/browser";
import type { PublicKeyCredentialRequestOptionsJSON } from "@simplewebauthn/browser";
import { getNetworkOrServerError } from "@/utils/http";

function getLoginError(error: unknown): string {
  const networkOrServer = getNetworkOrServerError(error);
  if (networkOrServer) return networkOrServer;
  if (isAxiosError(error)) {
    if (error.response?.status === 401) return "Invalid username or password";
    const detail = error.response?.data?.detail;
    if (typeof detail === "string") return detail;
  }
  return "Sign in failed. Please try again.";
}

export default function LoginPage() {
  const { login, refetch } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);

    try {
      await login({ username, password });
      navigate("/");
    } catch (err) {
      setError(getLoginError(err));
    } finally {
      setLoading(false);
    }
  };

  const handlePasskeyLogin = async () => {
    setError("");
    setLoading(true);
    try {
      const options = await webAuthnLoginBegin();
      const result = await startAuthentication(
        { optionsJSON: options as PublicKeyCredentialRequestOptionsJSON },
      );
      await webAuthnLoginFinish(result);
      refetch();
      navigate("/");
    } catch (err) {
      const networkOrServer = getNetworkOrServerError(err);
      setError(networkOrServer ?? "Passkey authentication failed");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="w-full max-w-sm">
        <div className="bg-white rounded-lg shadow-sm border border-gray-200 p-6">
          <h1 data-testid="login-title" className="text-xl font-semibold text-gray-900 text-center mb-1">
            Sign In
          </h1>
          <p className="text-sm text-gray-500 text-center mb-6">
            OpenClaw Orchestrator
          </p>

          {error && (
            <div data-testid="login-error" className="mb-4 p-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded-md">
              {error}
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Username
              </label>
              <input
                data-testid="username-input"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                required
                autoFocus
                autoComplete="username"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Password
              </label>
              <input
                data-testid="password-input"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                required
                autoComplete="current-password"
              />
            </div>
            <button
              data-testid="login-submit-button"
              type="submit"
              disabled={loading}
              className="w-full px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {loading ? "Please wait..." : "Sign In"}
            </button>
          </form>

          <div className="relative my-4">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-gray-200" />
            </div>
            <div className="relative flex justify-center text-xs">
              <span className="bg-white px-2 text-gray-400">or</span>
            </div>
          </div>
          <button
            data-testid="passkey-login-button"
            onClick={handlePasskeyLogin}
            disabled={loading}
            className="w-full px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 flex items-center justify-center gap-2"
          >
            <Fingerprint size={16} />
            Sign in with Passkey
          </button>
        </div>
      </div>
    </div>
  );
}
