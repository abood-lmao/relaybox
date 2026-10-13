import { useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

export type LiveStatus = "connecting" | "live" | "offline";

interface LiveState {
  status: LiveStatus;
  /** ID of the newest request announced over the stream. */
  latestRequestId: number | null;
}

const LiveContext = createContext<LiveState>({ status: "offline", latestRequestId: null });

const RECONNECT_MS = 10_000;

/**
 * Subscribes to /api/v1/events and refreshes cached queries when data
 * changes. If the stream is unavailable, pages fall back to polling (see
 * usePollInterval).
 */
export function LiveProvider({ children, enabled = true }: { children: ReactNode; enabled?: boolean }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState<LiveStatus>(() =>
    enabled && typeof EventSource !== "undefined" ? "connecting" : "offline",
  );
  const [latestRequestId, setLatestRequestId] = useState<number | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") return;
    const es = new EventSource("/api/v1/events");
    const pending = new Set<string>();
    let flushTimer: ReturnType<typeof setTimeout> | undefined;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    const invalidate = (...keys: string[]) => {
      keys.forEach((k) => pending.add(k));
      if (flushTimer) return;
      flushTimer = setTimeout(() => {
        flushTimer = undefined;
        pending.forEach((k) => void qc.invalidateQueries({ queryKey: [k] }));
        pending.clear();
      }, 250);
    };

    es.onopen = () => setStatus("live");
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) {
        // The browser gives up after a non-200 answer; try again later.
        setStatus("offline");
        retryTimer = setTimeout(() => setAttempt((a) => a + 1), RECONNECT_MS);
      } else {
        setStatus("connecting");
      }
    };
    es.addEventListener("request.created", (e) => {
      try {
        const data = JSON.parse((e as MessageEvent<string>).data) as { id?: number };
        if (typeof data.id === "number") setLatestRequestId(data.id);
      } catch {
        // Ignore malformed events; the refresh below still runs.
      }
      invalidate("requests", "stats", "endpoints");
    });
    es.addEventListener("requests.imported", () => invalidate("requests", "stats", "endpoints"));
    es.addEventListener("requests.deleted", () => invalidate("requests", "request", "stats", "endpoints"));
    es.addEventListener("endpoints.changed", () => invalidate("endpoints", "stats", "requests"));
    es.addEventListener("settings.updated", () => invalidate("settings", "meta", "endpoints"));
    es.addEventListener("resync", () => void qc.invalidateQueries());

    return () => {
      es.close();
      clearTimeout(flushTimer);
      clearTimeout(retryTimer);
    };
  }, [qc, enabled, attempt]);

  return <LiveContext.Provider value={{ status, latestRequestId }}>{children}</LiveContext.Provider>;
}

// eslint-disable-next-line react-refresh/only-export-components
export function useLive(): LiveState {
  return useContext(LiveContext);
}

/** Polling interval for list queries: off while live updates work. */
// eslint-disable-next-line react-refresh/only-export-components
export function usePollInterval(ms = 10_000): number | false {
  return useLive().status === "live" ? false : ms;
}
