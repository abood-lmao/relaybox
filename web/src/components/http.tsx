import { Repeat2, Upload } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { REDACTED, STATUS_TEXT, statusClass } from "../lib/http";
import type { Source } from "../types/api";

const methodColor: Record<string, string> = {
  GET: "text-m-get",
  HEAD: "text-m-get",
  POST: "text-m-post",
  PUT: "text-m-put",
  PATCH: "text-m-patch",
  DELETE: "text-m-delete",
};

export function MethodTag({ method, className }: { method: string; className?: string }) {
  return (
    <span
      className={cn(
        "font-mono text-xs font-medium tracking-wide",
        methodColor[method] ?? "text-m-other",
        className,
      )}
    >
      {method}
    </span>
  );
}

const statusColor = {
  "1xx": "text-muted",
  "2xx": "text-ok",
  "3xx": "text-redirect",
  "4xx": "text-warn",
  "5xx": "text-err",
};

export function StatusCode({
  status,
  withText,
  className,
}: {
  status: number;
  withText?: boolean;
  className?: string;
}) {
  return (
    <span className={cn("font-mono text-xs font-medium", statusColor[statusClass(status)], className)}>
      {status}
      {withText && STATUS_TEXT[status] && (
        <span className="ml-1.5 font-sans font-normal">{STATUS_TEXT[status]}</span>
      )}
    </span>
  );
}

export function StatusDot({ status }: { status: number }) {
  const c = statusClass(status);
  return (
    <span
      aria-hidden
      className={cn(
        "inline-block size-1.5 rounded-full",
        c === "2xx" && "bg-ok",
        c === "3xx" && "bg-redirect",
        c === "4xx" && "bg-warn",
        c === "5xx" && "bg-err",
        c === "1xx" && "bg-muted",
      )}
    />
  );
}

export function SourceTag({ source }: { source: Source }) {
  if (source === "capture") return null;
  const Icon = source === "replay" ? Repeat2 : Upload;
  return (
    <span
      className="inline-flex items-center gap-1 rounded border border-line px-1 text-[10px] font-medium tracking-wide text-muted uppercase"
      title={source === "replay" ? "Created by a replay" : "Imported from a fixture"}
    >
      <Icon className="size-2.5" aria-hidden />
      {source}
    </span>
  );
}

/**
 * Renders text with every redaction placeholder drawn as a hatched mark.
 * Output is plain React text, so captured markup is never interpreted.
 */
export function WithRedactions({ text }: { text: string }): ReactNode {
  if (!text.includes(REDACTED)) return text;
  const pieces = text.split(REDACTED);
  return pieces.map((piece, i) => (
    <span key={i}>
      {piece}
      {i < pieces.length - 1 && (
        <span className="redacted" title="Removed before storage">
          {REDACTED}
        </span>
      )}
    </span>
  ));
}
