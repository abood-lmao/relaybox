import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { LogoMark } from "../components/Logo";
import { Button } from "../components/ui/button";
import { Field, Input } from "../components/ui/form";
import { api, errorMessage } from "../lib/api";

export function LoginPage() {
  useDocumentTitle("Sign in");
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const login = useMutation({
    mutationFn: (t: string) => api.login(t),
    onSuccess: (session) => {
      setToken("");
      qc.setQueryData(["session"], session);
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (token.trim()) login.mutate(token.trim());
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-sidebar px-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm rounded-lg border border-line bg-surface p-6 shadow-2xl"
      >
        <div className="mb-5 flex items-center gap-3">
          <LogoMark className="size-9" />
          <div>
            <h1 className="text-base font-semibold">Sign in to Relaybox</h1>
            <p className="text-sm text-muted">This dashboard is protected by an access token.</p>
          </div>
        </div>
        <Field
          label="Access token"
          hint="The value of RELAYBOX_DASHBOARD_TOKEN on the server."
          error={login.isError ? errorMessage(login.error) : undefined}
        >
          {(p) => (
            <Input
              {...p}
              type="password"
              autoComplete="current-password"
              autoFocus
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
          )}
        </Field>
        <Button
          type="submit"
          variant="primary"
          className="mt-5 w-full"
          loading={login.isPending}
          disabled={!token.trim()}
          icon={<KeyRound className="size-3.5" aria-hidden />}
        >
          Sign in
        </Button>
      </form>
    </div>
  );
}
