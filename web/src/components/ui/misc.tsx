import { Check, Copy } from "lucide-react";
import type { ReactNode } from "react";
import { useCopy } from "../../hooks/useCopy";
import { cn } from "../../lib/cn";
import { useToast } from "../toast";

export function Panel({
  title,
  actions,
  children,
  className,
  bodyClassName,
  description,
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  description?: ReactNode;
}) {
  return (
    <section className={cn("rounded-lg border border-line bg-surface", className)}>
      {(title || actions) && (
        <header className="flex min-h-11 flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2">
          <div className="min-w-0">
            {title && <h2 className="text-sm font-semibold">{title}</h2>}
            {description && <p className="text-xs text-muted">{description}</p>}
          </div>
          {actions && <div className="flex items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={bodyClassName}>{children}</div>
    </section>
  );
}

type BadgeTone = "neutral" | "accent" | "ok" | "warn" | "err";

export function Badge({
  tone = "neutral",
  children,
  className,
}: {
  tone?: BadgeTone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded px-1.5 py-px text-[11px] font-medium whitespace-nowrap",
        tone === "neutral" && "bg-surface-2 text-muted",
        tone === "accent" && "bg-accent-soft text-accent-fg",
        tone === "ok" && "bg-ok/12 text-ok",
        tone === "warn" && "bg-warn/12 text-warn",
        tone === "err" && "bg-err/12 text-err",
        className,
      )}
    >
      {children}
    </span>
  );
}

export function CopyButton({
  text,
  label = "Copy",
  size = "sm",
  showLabel = false,
  className,
}: {
  text: string;
  label?: string;
  size?: "sm" | "md";
  showLabel?: boolean;
  className?: string;
}) {
  const [copied, copy] = useCopy();
  const toast = useToast();
  const onClick = async () => {
    if (!(await copy(text))) toast.error("Copying failed. Select the text and copy it manually.");
  };
  const Icon = copied ? Check : Copy;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={showLabel ? undefined : label}
      title={label}
      className={cn(
        "inline-flex shrink-0 items-center justify-center gap-1.5 rounded-md text-muted transition-colors hover:bg-surface-2 hover:text-fg",
        showLabel ? "h-7 px-2 text-xs font-medium" : size === "sm" ? "size-6" : "size-8",
        copied && "text-ok hover:text-ok",
        className,
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      {showLabel && <span>{copied ? "Copied" : label}</span>}
      <span className="sr-only" aria-live="polite">
        {copied ? "Copied" : ""}
      </span>
    </button>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border border-line-strong bg-surface-2 px-1 font-mono text-[10px] text-muted">
      {children}
    </kbd>
  );
}

/** Definition list used for metadata grids. */
export function Facts({ items, className }: { items: [ReactNode, ReactNode][]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[minmax(110px,max-content)_1fr] gap-x-6 gap-y-2 text-sm", className)}>
      {items.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v}</dd>
        </div>
      ))}
    </dl>
  );
}
