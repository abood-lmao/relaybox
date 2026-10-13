import { twMerge } from "tailwind-merge";

/**
 * Joins class names, skipping falsy values. Conflicting Tailwind utilities
 * resolve in favour of the later one, so callers can override defaults.
 */
export function cn(...classes: (string | false | null | undefined)[]): string {
  return twMerge(classes.filter(Boolean).join(" "));
}
