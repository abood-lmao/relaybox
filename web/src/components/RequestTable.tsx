import { Link } from "react-router";
import { cn } from "../lib/cn";
import { formatBytes, formatDateTime, formatDuration, formatTimestamp } from "../lib/format";
import { displayQuery } from "../lib/http";
import type { RequestSummary } from "../types/api";
import { MethodTag, SourceTag, StatusCode, WithRedactions } from "./http";
import { Checkbox } from "./ui/form";

interface Props {
  items: RequestSummary[];
  showEndpoint?: boolean;
  compact?: boolean;
  selected?: Set<number>;
  onToggle?: (id: number) => void;
  onToggleAll?: (checked: boolean) => void;
  /** Rows with a higher ID are marked as newly arrived. */
  newerThan?: number;
}

export function RequestTable({
  items,
  showEndpoint = true,
  compact,
  selected,
  onToggle,
  onToggleAll,
  newerThan,
}: Props) {
  const isNew = (id: number) => newerThan !== undefined && id > newerThan;
  const selectable = !!selected && !!onToggle;
  const allSelected = selectable && items.length > 0 && items.every((r) => selected.has(r.id));
  const cell = compact ? "px-3 py-2" : "px-3 py-2.5";

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] text-left text-sm">
        <thead className="border-b border-line text-xs text-muted">
          <tr>
            {selectable && (
              <th scope="col" className="w-9 py-2 pl-4">
                <Checkbox
                  checked={allSelected}
                  onChange={(e) => onToggleAll?.(e.target.checked)}
                  aria-label="Select all requests on this page"
                />
              </th>
            )}
            <th scope="col" className={cn("font-medium", cell, !selectable && "pl-4")}>
              Received
            </th>
            <th scope="col" className={cn("font-medium", cell)}>
              Method
            </th>
            <th scope="col" className={cn("w-full font-medium", cell)}>
              Path
            </th>
            {showEndpoint && (
              <th scope="col" className={cn("font-medium", cell)}>
                Endpoint
              </th>
            )}
            <th scope="col" className={cn("font-medium", cell)}>
              Status
            </th>
            {!compact && (
              <>
                <th scope="col" className={cn("text-right font-medium", cell)}>
                  Size
                </th>
                <th scope="col" className={cn("pr-4 text-right font-medium", cell)}>
                  Time
                </th>
              </>
            )}
          </tr>
        </thead>
        <tbody>
          {items.map((r) => {
            const checked = selected?.has(r.id) ?? false;
            return (
              <tr
                key={r.id}
                className={cn(
                  "relative border-b border-line last:border-0 hover:bg-surface-2/70",
                  checked && "bg-accent-soft/60",
                  isNew(r.id) && "row-new",
                )}
              >
                {selectable && (
                  <td className="relative z-10 w-9 py-2 pl-4">
                    <Checkbox
                      checked={checked}
                      onChange={() => onToggle(r.id)}
                      aria-label={`Select request ${r.id}`}
                    />
                  </td>
                )}
                <td className={cn("whitespace-nowrap text-muted", cell, !selectable && "pl-4")}>
                  <time
                    dateTime={r.received_at}
                    title={formatDateTime(r.received_at)}
                    className="font-mono text-xs"
                  >
                    {formatTimestamp(r.received_at)}
                  </time>
                </td>
                <td className={cell}>
                  <MethodTag method={r.method} />
                </td>
                <td className={cn("max-w-0", cell)}>
                  <div className="flex items-center gap-2">
                    <Link
                      to={`/requests/${r.id}`}
                      className="min-w-0 truncate font-mono text-[12.5px] after:absolute after:inset-0 hover:text-accent-fg focus-visible:outline-none focus-visible:after:outline-2 focus-visible:after:outline-accent"
                      title={r.path + (r.query_string ? `?${displayQuery(r.query_string)}` : "")}
                    >
                      {r.path || "/"}
                      {r.query_string && (
                        <span className="text-faint">
                          ?<WithRedactions text={displayQuery(r.query_string)} />
                        </span>
                      )}
                    </Link>
                    <SourceTag source={r.source} />
                    {r.redacted && (
                      <span
                        className="redacted shrink-0 text-[10px]! leading-4"
                        title="Some values were redacted before storage"
                      >
                        redacted
                      </span>
                    )}
                    {r.body_truncated && (
                      <span
                        className="shrink-0 text-[10px] font-medium text-warn uppercase"
                        title="Body truncated at the size limit"
                      >
                        truncated
                      </span>
                    )}
                  </div>
                </td>
                {showEndpoint && (
                  <td
                    className={cn("max-w-44 truncate whitespace-nowrap text-muted", cell)}
                    title={r.endpoint.name}
                  >
                    {r.endpoint.name}
                  </td>
                )}
                <td className={cell}>
                  <StatusCode status={r.status} />
                </td>
                {!compact && (
                  <>
                    <td className={cn("text-right font-mono text-xs whitespace-nowrap text-muted", cell)}>
                      {formatBytes(r.body_size)}
                    </td>
                    <td
                      className={cn("pr-4 text-right font-mono text-xs whitespace-nowrap text-muted", cell)}
                    >
                      {formatDuration(r.duration_ms)}
                    </td>
                  </>
                )}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
