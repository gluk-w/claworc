import { ServerCrash } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";

export default function BackendUnavailablePage() {
  const { refetch } = useAuth();
  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="w-full max-w-md">
        <div className="bg-white rounded-lg shadow-sm border border-gray-200 p-6 text-center">
          <div className="flex justify-center mb-4 text-red-500">
            <ServerCrash size={40} />
          </div>
          <h1 className="text-xl font-semibold text-gray-900 mb-1">
            503 — Backend unavailable
          </h1>
          <p className="text-sm text-gray-500 mb-6">
            The Claworc backend is not reachable. It may still be starting up.
          </p>
          <button
            type="button"
            onClick={() => refetch()}
            className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
          >
            Retry
          </button>
        </div>
      </div>
    </div>
  );
}
