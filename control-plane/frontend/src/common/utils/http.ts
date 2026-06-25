import { isAxiosError } from "axios";

export function isBackendUnavailableError(error: unknown): boolean {
  if (!isAxiosError(error)) return false;
  if (!error.response || error.code === "ERR_NETWORK") return true;
  const status = error.response.status;
  return status === 502 || status === 503 || status === 504;
}

export function getNetworkOrServerError(error: unknown): string | null {
  if (typeof navigator !== "undefined" && navigator.onLine === false) {
    return "You appear to be offline. Please check your internet connection.";
  }
  if (!isAxiosError(error)) return null;
  if (!error.response || error.code === "ERR_NETWORK") {
    return "Unable to reach the server. If you connect via VPN, make sure it's enabled.";
  }
  const status = error.response.status;
  if (status === 403) {
    return "Access denied by the network. If you connect via VPN, make sure it's enabled, otherwise contact your administrator.";
  }
  if (status === 502 || status === 503 || status === 504) {
    return "The server is temporarily unavailable. Please try again in a moment.";
  }
  if (status >= 500) {
    return "Something went wrong on the server. Please try again later.";
  }
  return null;
}
