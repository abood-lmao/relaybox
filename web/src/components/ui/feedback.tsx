import { CircleAlert, Info, LoaderCircle, RefreshCw, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { errorMessage } from "../../lib/api";
import { cn } from "../../lib/cn";
import { Button } from "./button";

export function Spinner({ label = "Loading", className }: { label?: string; className?: string }) {
  return (
    <div className={cn("flex items-center gap-2 text-sm text-muted", className)} role="status">
      <LoaderCircle className="size-4 animate-spin" aria-hidden />
      <span>{label}</span>
    </div>
  );
}

export function LoadingBlock({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex min-h-40 items-center justify-center">
      <Spinner label={label} />
    </div>
  );
}

interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}

export function EmptyState({ icon, title, children, action, className }: EmptyStateProps) {
  return (
    <div className={cn("flex flex-col items-center px-6 py-12 text-center", className)}>
      {icon && <div className="mb-3 text-faint">{icon}</div>}
      <h3 className="text-sm font-semibold">{title}</h3>
      {children && <div className="mt-1.5 max-w-md text-sm text-muted">{children}</div>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function ErrorState({
  error,
  onRetry,
  title = "This could not be loaded",
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
}) {
  return (
    <div className="flex flex-col items-center px-6 py-12 text-center" role="alert">
      <CircleAlert className="mb-3 size-6 text-err" aria-hidden />
      <h3 className="text-sm font-semibold">{title}</h3>
      <p className="mt-1.5 max-w-md text-sm text-muted">{errorMessage(error)}</p>
      {onRetry && (
        <Button
          className="mt-4"
          size="sm"
          icon={<RefreshCw className="size-3.5" aria-hidden />}
          onClick={onRetry}
        >
          Try again
        </Button>
      )}
    </div>
  );
}

type Tone = "info" | "warn" | "error";

export function Notice({
  tone = "info",
  title,
  children,
  className,
}: {
  tone?: Tone;
  title?: string;
  children?: ReactNode;
  className?: string;
}) {
  const Icon = tone === "info" ? Info : tone === "warn" ? TriangleAlert : CircleAlert;
  return (
    <div
      className={cn(
        "flex gap-2.5 rounded-md border px-3 py-2.5 text-sm",
        tone === "info" && "border-line bg-surface-2 text-fg",
        tone === "warn" && "border-warn/35 bg-warn/8 text-fg",
        tone === "error" && "border-err/35 bg-err/8 text-fg",
        className,
      )}
      role={tone === "error" ? "alert" : undefined}
    >
      <Icon
        className={cn(
          "mt-0.5 size-4 shrink-0",
          tone === "info" && "text-muted",
          tone === "warn" && "text-warn",
          tone === "error" && "text-err",
        )}
        aria-hidden
      />
      <div className="min-w-0 flex-1 leading-relaxed">
        {title && <p className="font-medium">{title}</p>}
        {children && <div className={cn(title && "mt-0.5", "text-muted")}>{children}</div>}
      </div>
    </div>
  );
}
