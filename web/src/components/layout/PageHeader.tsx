import { ArrowLeft } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";

interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  back?: { to: string; label: string };
  meta?: ReactNode;
}

export function PageHeader({ title, description, actions, back, meta }: PageHeaderProps) {
  return (
    <div className="mb-5">
      {back && (
        <Link to={back.to} className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-fg">
          <ArrowLeft className="size-3.5" aria-hidden />
          {back.label}
        </Link>
      )}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-lg font-semibold tracking-tight break-words">{title}</h1>
          {description && <p className="mt-0.5 text-sm text-muted">{description}</p>}
          {meta && (
            <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
              {meta}
            </div>
          )}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}
