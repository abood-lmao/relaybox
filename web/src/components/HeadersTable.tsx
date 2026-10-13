import { useState } from "react";
import { isRedacted } from "../lib/http";
import type { Pair } from "../types/api";
import { WithRedactions } from "./http";
import { EmptyState } from "./ui/feedback";
import { Input } from "./ui/form";
import { CopyButton } from "./ui/misc";

interface Props {
  pairs: Pair[];
  empty?: string;
  nameLabel?: string;
  filterable?: boolean;
}

/** Name/value table for headers, query parameters, and form fields. */
export function PairsTable({ pairs, empty = "Nothing here.", nameLabel = "Name", filterable }: Props) {
  const [filter, setFilter] = useState("");
  if (pairs.length === 0) return <EmptyState title={empty} className="py-8" />;
  const f = filter.trim().toLowerCase();
  const rows = f
    ? pairs.filter(([k, v]) => k.toLowerCase().includes(f) || v.toLowerCase().includes(f))
    : pairs;
  return (
    <div>
      {filterable && pairs.length > 10 && (
        <div className="border-b border-line px-4 py-2">
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={`Filter ${pairs.length} entries`}
            aria-label="Filter entries"
            className="max-w-xs"
          />
        </div>
      )}
      <table className="w-full table-fixed text-left">
        <thead className="sr-only">
          <tr>
            <th scope="col">{nameLabel}</th>
            <th scope="col">Value</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([k, v], i) => (
            <tr key={`${k}-${i}`} className="group border-b border-line last:border-0 hover:bg-surface-2/60">
              <th
                scope="row"
                className="w-[32%] max-w-xs px-4 py-1.5 align-top font-mono text-[12.5px] font-normal break-all text-muted"
              >
                {k || <span className="text-faint italic">(empty)</span>}
              </th>
              <td className="px-4 py-1.5 align-top font-mono text-[12.5px] break-all">
                <div className="flex items-start gap-2">
                  <span className="min-w-0 flex-1 whitespace-pre-wrap">
                    {v === "" ? (
                      <span className="text-faint italic">(empty)</span>
                    ) : (
                      <WithRedactions text={v} />
                    )}
                  </span>
                  {!isRedacted(v) && v !== "" && (
                    <CopyButton
                      text={v}
                      label={`Copy ${k}`}
                      className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                    />
                  )}
                </div>
              </td>
            </tr>
          ))}
          {rows.length === 0 && (
            <tr>
              <td colSpan={2} className="px-4 py-6 text-center text-sm text-muted">
                No entries match.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
