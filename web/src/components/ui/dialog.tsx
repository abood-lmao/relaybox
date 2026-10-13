import { X } from "lucide-react";
import { useEffect, useRef, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Button, IconButton } from "./button";

interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}

/**
 * Modal dialog built on <dialog>, which provides focus containment, Escape
 * to close, and an inert background.
 */
export function Dialog({ open, onClose, title, description, children, footer, wide }: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  });

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) el.showModal();
    if (!open && el.open) el.close();
  }, [open]);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const handleCancel = (e: Event) => {
      e.preventDefault();
      onCloseRef.current();
    };
    el.addEventListener("cancel", handleCancel);
    return () => el.removeEventListener("cancel", handleCancel);
  }, []);

  return (
    <dialog
      ref={ref}
      aria-labelledby={open ? "dialog-title" : undefined}
      className={cn(
        "m-auto w-[calc(100vw-2rem)] rounded-lg border border-line bg-surface p-0 text-fg shadow-2xl",
        wide ? "max-w-2xl" : "max-w-md",
      )}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      {open && (
        <div className="flex max-h-[85vh] flex-col">
          <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-3.5">
            <div>
              <h2 id="dialog-title" className="text-sm font-semibold">
                {title}
              </h2>
              {description && <div className="mt-1 text-sm text-muted">{description}</div>}
            </div>
            <IconButton label="Close" size="sm" onClick={onClose} className="-mr-1.5">
              <X className="size-4" aria-hidden />
            </IconButton>
          </div>
          {children && <div className="scroll-thin overflow-y-auto px-5 py-4">{children}</div>}
          {footer && (
            <div className="flex justify-end gap-2 border-t border-line bg-surface-2/50 px-5 py-3">
              {footer}
            </div>
          )}
        </div>
      )}
    </dialog>
  );
}

interface ConfirmProps {
  open: boolean;
  title: string;
  children?: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  danger,
  busy,
  onConfirm,
  onCancel,
}: ConfirmProps) {
  return (
    <Dialog
      open={open}
      onClose={onCancel}
      title={title}
      footer={
        <>
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant={danger ? "danger" : "primary"} loading={busy} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      {children && <div className="text-sm leading-relaxed text-muted">{children}</div>}
    </Dialog>
  );
}
