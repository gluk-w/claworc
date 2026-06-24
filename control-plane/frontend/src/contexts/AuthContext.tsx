import {
  createContext,
  useContext,
  useCallback,
  type ReactNode,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getAuthConfig,
  getCurrentUser,
  login as apiLogin,
  logout as apiLogout,
} from "@/api/auth";
import type { User, LoginRequest } from "@/types/auth";
import { isBackendUnavailableError } from "@/utils/http";

interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  isAdmin: boolean;
  canCreateInstances: boolean;
  isBackendUnavailable: boolean;
  // Cloudflare Access (Zero Trust) mode. When enabled, the built-in login is
  // replaced and identity comes from Cloudflare's verified headers.
  cfAccessEnabled: boolean;
  cfConfigLoading: boolean;
  login: (data: LoginRequest) => Promise<User>;
  logout: () => Promise<void>;
  refetch: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();

  const {
    data: user,
    isLoading,
    error,
    refetch,
  } = useQuery({
    queryKey: ["auth", "me"],
    queryFn: getCurrentUser,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });

  const { data: authConfig, isLoading: cfConfigLoading } = useQuery({
    queryKey: ["auth", "config"],
    queryFn: getAuthConfig,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });

  const login = useCallback(
    async (data: LoginRequest) => {
      const u = await apiLogin(data);
      queryClient.setQueryData(["auth", "me"], u);
      return u;
    },
    [queryClient],
  );

  const logout = useCallback(async () => {
    // In Cloudflare Access mode there is no Claworc session to clear; logging
    // out means ending the Cloudflare Access session via its logout endpoint.
    if (authConfig?.cf_access_enabled && authConfig.logout_url) {
      queryClient.clear();
      window.location.href = authConfig.logout_url;
      return;
    }
    await apiLogout();
    queryClient.setQueryData(["auth", "me"], null);
    queryClient.clear();
  }, [queryClient, authConfig]);

  return (
    <AuthContext.Provider
      value={{
        user: user ?? null,
        isLoading,
        isAdmin: user?.role === "admin",
        // Admins always; otherwise users who manage at least one team.
        canCreateInstances:
          user?.role === "admin" ||
          (user?.teams ?? []).some((t) => t.role === "manager"),
        isBackendUnavailable: !user && isBackendUnavailableError(error),
        cfAccessEnabled: authConfig?.cf_access_enabled ?? false,
        cfConfigLoading,
        login,
        logout,
        refetch: () => {
          refetch();
        },
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
