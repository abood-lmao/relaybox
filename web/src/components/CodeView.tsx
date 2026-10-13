import { useMemo } from "react";
import { cn } from "../lib/cn";
import { REDACTED } from "../lib/http";
import { tokenizeJson, type TokenType } from "../lib/json";
import { WithRedactions } from "./http";

const HIGHLIGHT_LIMIT = 256 * 1024;

const tokenClass: Record<TokenType, string> = {
  key: "text-code-key",
  string: "text-code-string",
  number: "text-code-number",
  keyword: "text-code-keyword",
  punct: "text-code-punct",
  plain: "",
};

interface CodeViewProps {
  text: string;
  json?: boolean;
  wrap?: boolean;
  className?: string;
  label?: string;
}

/**
 * Read-only code block. Text is always rendered as React text nodes; JSON
 * gets token colors when it is small enough to highlight quickly.
 */
export function CodeView({ text, json, wrap = true, className, label }: CodeViewProps) {
  const tokens = useMemo(
    () => (json && text.length <= HIGHLIGHT_LIMIT ? tokenizeJson(text) : null),
    [text, json],
  );
  return (
    <pre
      aria-label={label}
      tabIndex={0}
      className={cn(
        "scroll-thin font-mono text-[12.5px] leading-[1.6] text-fg",
        wrap ? "break-all whitespace-pre-wrap" : "overflow-x-auto whitespace-pre",
        className,
      )}
    >
      {tokens ? (
        tokens.map((t, i) =>
          t.type === "plain" ? (
            <span key={i}>{t.text}</span>
          ) : (
            <span key={i} className={tokenClass[t.type]}>
              {t.text.includes(REDACTED) ? <WithRedactions text={t.text} /> : t.text}
            </span>
          ),
        )
      ) : (
        <WithRedactions text={text} />
      )}
    </pre>
  );
}
