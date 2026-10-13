import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ApiError, api, errorMessage } from "../../lib/api";
import type { Endpoint } from "../../types/api";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { Field, Input } from "../ui/form";

interface Props {
  open: boolean;
  onClose: () => void;
  onCreated: (endpoint: Endpoint) => void;
}

export function CreateEndpointDialog({ open, onClose, onCreated }: Props) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: (n: string) => api.createEndpoint({ name: n }),
    onSuccess: (ep) => {
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      void qc.invalidateQueries({ queryKey: ["stats"] });
      setName("");
      onCreated(ep);
    },
  });
  const err = create.error;
  const nameError = !err
    ? undefined
    : err instanceof ApiError && err.status === 422
      ? (err.fields.name ?? err.message)
      : errorMessage(err);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) create.mutate(name.trim());
  };
  const close = () => {
    create.reset();
    onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={close}
      title="New endpoint"
      description="An endpoint gives you a unique URL that records every request sent to it."
    >
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="Name"
          hint="For example the service that will send to it: Stripe test, GitHub app."
          error={nameError}
        >
          {(p) => (
            <Input
              {...p}
              autoFocus
              maxLength={80}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Payments staging"
            />
          )}
        </Field>
        <p className="text-xs text-muted">
          New endpoints answer <span className="font-mono">200</span> with{" "}
          <span className="font-mono">{'{"received":true}'}</span>. You can change the response after creating
          it.
        </p>
        <div className="flex justify-end gap-2">
          <Button onClick={close}>Cancel</Button>
          <Button type="submit" variant="primary" loading={create.isPending} disabled={!name.trim()}>
            Create endpoint
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
