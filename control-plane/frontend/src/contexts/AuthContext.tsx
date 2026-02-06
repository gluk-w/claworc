import {
  createContext,
  useContext,
  useCallback,
  type ReactNode,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getCurrentUser,
  login as apiLogin,
  logout as apiLogout,
} from "@/api/auth";
import type { User, LoginRequest } from "@/types/auth";

interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  isAdmin: boolean;
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
    refetch,
  } = useQuery({
    queryKey: ["auth", "me"],
    queryFn: getCurrentUser,
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
    await apiLogout();
    queryClient.setQueryData(["auth", "me"], null);
    queryClient.clear();
  }, [queryClient]);

  return (
    <AuthContext.Provider
      value={{
        user: user ?? null,
        isLoading,
        isAdmin: user?.role === "admin",
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
