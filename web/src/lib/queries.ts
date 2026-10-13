import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { usePollInterval } from "../hooks/live";
import type { RequestFilters } from "../types/api";
import { api } from "./api";

export function useMeta() {
  return useQuery({ queryKey: ["meta"], queryFn: api.meta, staleTime: 5 * 60_000 });
}

export function useStats() {
  const poll = usePollInterval(15_000);
  return useQuery({ queryKey: ["stats"], queryFn: api.stats, refetchInterval: poll });
}

export function useEndpoints() {
  const poll = usePollInterval(30_000);
  return useQuery({ queryKey: ["endpoints"], queryFn: api.listEndpoints, refetchInterval: poll });
}

export function useEndpoint(id: number) {
  return useQuery({ queryKey: ["endpoints", id], queryFn: () => api.getEndpoint(id), enabled: id > 0 });
}

export function useRequests(filters: RequestFilters) {
  const poll = usePollInterval(10_000);
  return useQuery({
    queryKey: ["requests", filters],
    queryFn: () => api.listRequests(filters),
    placeholderData: keepPreviousData,
    refetchInterval: poll,
  });
}

export function useRequest(id: number) {
  return useQuery({ queryKey: ["request", id], queryFn: () => api.getRequest(id), enabled: id > 0 });
}

export function useReplayHistory(id: number) {
  return useQuery({
    queryKey: ["request", id, "replays"],
    queryFn: () => api.replayHistory(id),
    enabled: id > 0,
  });
}

export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: api.settings });
}
