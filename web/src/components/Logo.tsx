import { cn } from "../lib/cn";

/** Box with a request passing through it. */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={cn("size-7 shrink-0", className)}>
      <rect width="32" height="32" rx="7" fill="#262a31" />
      <rect x="8.5" y="8.5" width="15" height="15" rx="2.5" fill="none" stroke="#7fa6f2" strokeWidth="2" />
      <path d="M3.5 16h13" stroke="#e2e6eb" strokeWidth="2" strokeLinecap="round" />
      <circle cx="21" cy="16" r="2.25" fill="#e2e6eb" />
    </svg>
  );
}

export function Logo() {
  return (
    <div className="flex items-center gap-2.5">
      <LogoMark />
      <span className="text-[15px] font-semibold tracking-tight text-white">Relaybox</span>
    </div>
  );
}
