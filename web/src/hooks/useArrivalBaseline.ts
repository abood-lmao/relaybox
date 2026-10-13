import { useState } from "react";
import type { RequestSummary } from "../types/api";

/**
 * Remembers the highest request ID in the first loaded result for a view,
 * so requests that arrive later can be highlighted.
 */
export function useArrivalBaseline(items: RequestSummary[] | undefined, viewKey: string, ready: boolean) {
  const [state, setState] = useState<{ key: string; max: number } | null>(null);
  if (ready && items && state?.key !== viewKey) {
    const max = items.reduce((m, r) => Math.max(m, r.id), 0);
    setState({ key: viewKey, max });
    return max;
  }
  return state?.key === viewKey ? state.max : undefined;
}
