import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import type { Session } from "../types/api";
import { ApiError } from "./api";

export function createQueryClient(): QueryClient {
  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({ onError: (err) => handleAuthError(err) }),
    mutationCache: new MutationCache({ onError: (err) => handleAuthError(err) }),
    defaultOptions: {
      queries: {
        staleTime: 5_000,
        refetchOnWindowFocus: true,
        retry: (count, err) => {
          if (err instanceof ApiError && err.status >= 400 && err.status < 500) return false;
          return count < 2;
        },
      },
    },
  });
  // A 401 from any call means the session ended; show the sign-in page.
  function handleAuthError(err: unknown) {
    if (err instanceof ApiError && err.status === 401) {
      client.setQueryData<Session>(["session"], (s) => (s ? { ...s, authenticated: false } : s));
    }
  }
  return client;
}
