import { ChevronLeft, ChevronRight } from "lucide-react";
import { formatNumber } from "../../lib/format";
import { IconButton } from "./button";
import { Select } from "./form";

interface Props {
  total: number;
  limit: number;
  offset: number;
  onChange: (offset: number, limit: number) => void;
  sizes?: number[];
}

export function Pagination({ total, limit, offset, onChange, sizes = [25, 50, 100, 200] }: Props) {
  const first = total === 0 ? 0 : offset + 1;
  const last = Math.min(offset + limit, total);
  const page = Math.floor(offset / limit) + 1;
  const pages = Math.max(1, Math.ceil(total / limit));
  return (
    <nav
      aria-label="Pagination"
      className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 text-xs text-muted"
    >
      <span>
        {formatNumber(first)}–{formatNumber(last)} of {formatNumber(total)}
      </span>
      <div className="flex items-center gap-3">
        <span className="flex items-center gap-2 whitespace-nowrap">
          <span aria-hidden>Per page</span>
          <Select
            aria-label="Rows per page"
            value={limit}
            onChange={(e) => onChange(0, Number(e.target.value))}
            className="h-7 w-20 text-xs"
          >
            {sizes.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        </span>
        <span>
          Page {formatNumber(page)} of {formatNumber(pages)}
        </span>
        <div className="flex">
          <IconButton
            label="Previous page"
            disabled={offset === 0}
            onClick={() => onChange(Math.max(0, offset - limit), limit)}
          >
            <ChevronLeft className="size-4" aria-hidden />
          </IconButton>
          <IconButton
            label="Next page"
            disabled={offset + limit >= total}
            onClick={() => onChange(offset + limit, limit)}
          >
            <ChevronRight className="size-4" aria-hidden />
          </IconButton>
        </div>
      </div>
    </nav>
  );
}
