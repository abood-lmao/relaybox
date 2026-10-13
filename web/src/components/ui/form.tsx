import {
  forwardRef,
  useId,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { cn } from "../../lib/cn";

const control =
  "w-full rounded-md border border-line-strong bg-surface px-2.5 text-sm text-fg placeholder:text-faint transition-colors focus:border-accent focus:outline-none focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-accent/40 disabled:opacity-60 aria-invalid:border-err";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className, ...rest },
  ref,
) {
  return <input ref={ref} className={cn(control, "h-8", className)} {...rest} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  function Textarea({ className, ...rest }, ref) {
    return <textarea ref={ref} className={cn(control, "py-2 leading-relaxed", className)} {...rest} />;
  },
);

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cn(control, "h-8 cursor-pointer pr-7", className)} {...rest}>
      {children}
    </select>
  );
}

interface FieldProps {
  label: ReactNode;
  hint?: ReactNode;
  error?: string;
  children: (props: { id: string; "aria-invalid"?: boolean; "aria-describedby"?: string }) => ReactNode;
  className?: string;
  aside?: ReactNode;
}

/** A labelled form control with optional hint and error text. */
export function Field({ label, hint, error, children, className, aside }: FieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : "", error ? errorId : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <div className="flex items-baseline justify-between gap-2">
        <label htmlFor={id} className="text-xs font-medium text-fg">
          {label}
        </label>
        {aside}
      </div>
      {children({ id, "aria-invalid": error ? true : undefined, "aria-describedby": describedBy })}
      {error ? (
        <p id={errorId} className="text-xs text-err">
          {error}
        </p>
      ) : hint ? (
        <p id={hintId} className="text-xs text-muted">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

interface SwitchProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  disabled?: boolean;
  showLabel?: boolean;
}

export function Switch({ checked, onChange, label, disabled, showLabel }: SwitchProps) {
  return (
    <label className={cn("inline-flex items-center gap-2", disabled ? "opacity-60" : "cursor-pointer")}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={showLabel ? undefined : label}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cn(
          "relative inline-flex h-[18px] w-8 shrink-0 items-center rounded-full border transition-colors",
          checked ? "border-accent bg-accent" : "border-line-strong bg-surface-2",
        )}
      >
        <span
          className={cn(
            "inline-block size-3 rounded-full bg-white shadow-sm transition-transform",
            checked ? "translate-x-[15px]" : "translate-x-[2px] dark:bg-muted",
          )}
        />
      </button>
      {showLabel && <span className="text-sm">{label}</span>}
    </label>
  );
}

export function Checkbox({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input type="checkbox" className={cn("size-3.5 cursor-pointer accent-accent", className)} {...rest} />
  );
}
