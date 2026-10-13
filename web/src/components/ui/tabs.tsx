import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { cn } from "../../lib/cn";

export interface TabItem<K extends string> {
  key: K;
  label: ReactNode;
  count?: number;
}

interface TabsProps<K extends string> {
  tabs: TabItem<K>[];
  value: K;
  onChange: (key: K) => void;
  label: string;
  size?: "md" | "sm";
  className?: string;
}

/** Tab list with arrow-key navigation (WAI-ARIA tabs pattern). */
export function Tabs<K extends string>({
  tabs,
  value,
  onChange,
  label,
  size = "md",
  className,
}: TabsProps<K>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const onKeyDown = (e: KeyboardEvent, index: number) => {
    let next = -1;
    if (e.key === "ArrowRight") next = (index + 1) % tabs.length;
    if (e.key === "ArrowLeft") next = (index - 1 + tabs.length) % tabs.length;
    if (e.key === "Home") next = 0;
    if (e.key === "End") next = tabs.length - 1;
    if (next >= 0) {
      e.preventDefault();
      onChange(tabs[next].key);
      refs.current[next]?.focus();
    }
  };
  return (
    <div
      role="tablist"
      aria-label={label}
      className={cn("flex gap-1 overflow-x-auto", size === "md" && "border-b border-line", className)}
    >
      {tabs.map((t, i) => {
        const selected = t.key === value;
        return (
          <button
            key={t.key}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            id={`tab-${t.key}`}
            aria-selected={selected}
            aria-controls={`panel-${t.key}`}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(t.key)}
            onKeyDown={(e) => onKeyDown(e, i)}
            className={cn(
              "inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap transition-colors",
              size === "md" &&
                cn(
                  "-mb-px border-b-2 px-3 pt-1 pb-2 text-sm",
                  selected
                    ? "border-accent font-medium text-fg"
                    : "border-transparent text-muted hover:text-fg",
                ),
              size === "sm" &&
                cn(
                  "rounded px-2 py-1 text-xs",
                  selected ? "bg-surface-2 font-medium text-fg" : "text-muted hover:text-fg",
                ),
            )}
          >
            {t.label}
            {t.count !== undefined && (
              <span className="rounded bg-surface-2 px-1 font-mono text-[11px] text-muted">{t.count}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

export function TabPanel({
  id,
  children,
  className,
}: {
  id: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div role="tabpanel" id={`panel-${id}`} aria-labelledby={`tab-${id}`} className={className}>
      {children}
    </div>
  );
}
