import { useMutation } from "@tanstack/react-query";
import { Eye, Plus, RotateCcw, Trash2 } from "lucide-react";
import { useState } from "react";
import { ApiError, api, errorMessage } from "../../lib/api";
import { STATUS_TEXT } from "../../lib/http";
import type { Meta, MockResponse, ResponseInfo } from "../../types/api";
import { BodyView } from "../BodyView";
import { StatusCode } from "../http";
import { PairsTable } from "../HeadersTable";
import { Button, IconButton } from "../ui/button";
import { Notice } from "../ui/feedback";
import { Field, Input, Textarea } from "../ui/form";

interface HeaderRow {
  key: number;
  name: string;
  value: string;
}

interface Draft {
  status: string;
  content_type: string;
  headers: HeaderRow[];
  body: string;
  delay_ms: string;
}

let rowKey = 0;

function toDraft(m: MockResponse): Draft {
  return {
    status: String(m.status),
    content_type: m.content_type,
    headers: Object.entries(m.headers ?? {}).map(([name, value]) => ({ key: ++rowKey, name, value })),
    body: m.body,
    delay_ms: String(m.delay_ms),
  };
}

function toMock(d: Draft): MockResponse {
  const headers: Record<string, string> = {};
  for (const h of d.headers) if (h.name.trim()) headers[h.name.trim()] = h.value;
  return {
    status: Number(d.status),
    content_type: d.content_type.trim(),
    headers,
    body: d.body,
    delay_ms: Number(d.delay_ms || 0),
  };
}

const CONTENT_TYPES = [
  "application/json",
  "text/plain",
  "application/xml",
  "text/xml",
  "application/x-www-form-urlencoded",
];

interface Props {
  value: MockResponse;
  meta: Meta;
  saving?: boolean;
  error?: unknown;
  onSave: (mock: MockResponse) => void;
}

/** Form for the canned response an endpoint returns to senders. */
export function MockResponseEditor({ value, meta, saving, error, onSave }: Props) {
  const [draft, setDraft] = useState(() => toDraft(value));
  const [base, setBase] = useState(value);
  const [preview, setPreview] = useState<ResponseInfo | null>(null);

  // Reload the form when the saved value changes underneath it.
  if (value !== base) {
    setBase(value);
    setDraft(toDraft(value));
  }

  const previewMutation = useMutation({
    mutationFn: (m: MockResponse) => api.previewMock(m),
    onSuccess: setPreview,
  });

  const fieldErrors: Record<string, string> = {};
  for (const err of [error, previewMutation.error]) {
    if (err instanceof ApiError) {
      for (const [k, v] of Object.entries(err.fields)) {
        if (k.startsWith("mock_response.")) fieldErrors[k.slice("mock_response.".length)] ??= v;
      }
    }
  }
  const generalError =
    error && !(error instanceof ApiError && error.status === 422) ? errorMessage(error) : undefined;

  const set = (patch: Partial<Draft>) => {
    setDraft((d) => ({ ...d, ...patch }));
    setPreview(null);
  };
  const setHeader = (key: number, patch: Partial<HeaderRow>) =>
    set({ headers: draft.headers.map((h) => (h.key === key ? { ...h, ...patch } : h)) });

  const dirty = JSON.stringify(toMock(draft)) !== JSON.stringify(toMock(toDraft(value)));
  const statusNum = Number(draft.status);
  const statusText = STATUS_TEXT[statusNum];

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave(toMock(draft));
      }}
      className="space-y-4"
    >
      <div className="grid gap-4 sm:grid-cols-[140px_1fr_140px]">
        <Field
          label="Status"
          error={fieldErrors.status}
          hint={statusText ?? (draft.status ? "Custom status" : undefined)}
        >
          {(p) => (
            <Input
              {...p}
              inputMode="numeric"
              value={draft.status}
              onChange={(e) => set({ status: e.target.value.replace(/\D/g, "").slice(0, 3) })}
              className="font-mono"
            />
          )}
        </Field>
        <Field label="Content type" error={fieldErrors.content_type} hint="Leave empty to send text/plain.">
          {(p) => (
            <>
              <Input
                {...p}
                list="mock-content-types"
                value={draft.content_type}
                onChange={(e) => set({ content_type: e.target.value })}
                className="font-mono"
              />
              <datalist id="mock-content-types">
                {CONTENT_TYPES.map((t) => (
                  <option key={t} value={t} />
                ))}
              </datalist>
            </>
          )}
        </Field>
        <Field
          label="Delay (ms)"
          error={fieldErrors.delay_ms}
          hint={`Up to ${meta.limits.mock_delay_ms.toLocaleString()} ms`}
        >
          {(p) => (
            <Input
              {...p}
              inputMode="numeric"
              value={draft.delay_ms}
              onChange={(e) => set({ delay_ms: e.target.value.replace(/\D/g, "").slice(0, 6) })}
              className="font-mono"
            />
          )}
        </Field>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-xs font-medium">Headers</legend>
        {draft.headers.length === 0 && <p className="text-xs text-muted">No extra headers.</p>}
        {draft.headers.map((h) => (
          <div key={h.key} className="flex gap-2">
            <Input
              aria-label="Header name"
              placeholder="X-Example"
              value={h.name}
              onChange={(e) => setHeader(h.key, { name: e.target.value })}
              className="w-2/5 font-mono"
            />
            <Input
              aria-label={`Value for ${h.name || "header"}`}
              placeholder="value"
              value={h.value}
              onChange={(e) => setHeader(h.key, { value: e.target.value })}
              className="flex-1 font-mono"
            />
            <IconButton
              label="Remove header"
              onClick={() => set({ headers: draft.headers.filter((x) => x.key !== h.key) })}
            >
              <Trash2 className="size-3.5" aria-hidden />
            </IconButton>
          </div>
        ))}
        {fieldErrors.headers && <p className="text-xs text-err">{fieldErrors.headers}</p>}
        <Button
          size="sm"
          variant="ghost"
          icon={<Plus className="size-3.5" aria-hidden />}
          disabled={draft.headers.length >= meta.limits.mock_headers}
          onClick={() => set({ headers: [...draft.headers, { key: ++rowKey, name: "", value: "" }] })}
        >
          Add header
        </Button>
      </fieldset>

      <Field
        label="Body"
        error={fieldErrors.body}
        hint={`Up to ${(meta.limits.mock_body_bytes / 1024).toLocaleString()} KiB. JSON bodies are checked before saving.`}
      >
        {(p) => (
          <Textarea
            {...p}
            rows={6}
            spellCheck={false}
            value={draft.body}
            onChange={(e) => set({ body: e.target.value })}
            className="font-mono text-[12.5px]"
          />
        )}
      </Field>

      {generalError && <Notice tone="error">{generalError}</Notice>}
      {previewMutation.error &&
        !(previewMutation.error instanceof ApiError && previewMutation.error.status === 422) && (
          <Notice tone="error">{errorMessage(previewMutation.error)}</Notice>
        )}

      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="primary" loading={saving} disabled={!dirty || statusNum === 0}>
          Save response
        </Button>
        <Button
          icon={<Eye className="size-3.5" aria-hidden />}
          loading={previewMutation.isPending}
          onClick={() => previewMutation.mutate(toMock(draft))}
        >
          Preview
        </Button>
        <Button
          variant="ghost"
          icon={<RotateCcw className="size-3.5" aria-hidden />}
          onClick={() => set(toDraft(meta.mock_defaults))}
        >
          Use defaults
        </Button>
        {dirty && <span className="text-xs text-muted">Unsaved changes</span>}
      </div>

      {preview && (
        <div className="rounded-md border border-line">
          <div className="flex items-center gap-2 border-b border-line px-4 py-2 text-xs text-muted">
            <span>A sender would receive</span>
            <StatusCode status={preview.status} withText />
          </div>
          <PairsTable pairs={preview.headers} empty="No headers" />
          <div className="border-t border-line">
            <BodyView body={preview.body} noun="response body" />
          </div>
        </div>
      )}
    </form>
  );
}
