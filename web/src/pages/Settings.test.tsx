import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stats } from "../test/fixtures";
import { mockApi, renderApp } from "../test/utils";
import type { RuntimeSettings, Settings, SettingsPatch } from "../types/api";

afterEach(() => vi.unstubAllGlobals());

const defaults: RuntimeSettings = {
  retention_days: 7,
  max_stored_requests: 10000,
  default_max_body_bytes: 1048576,
  redact_headers: ["Authorization", "Cookie"],
  redact_fields: ["password", "token"],
};

function settings(runtime: Partial<RuntimeSettings> = {}, overridden: Settings["overridden"] = []): Settings {
  return {
    runtime: { ...defaults, ...runtime },
    defaults,
    overridden,
    limits: {
      min_body_bytes: 1024,
      max_body_bytes_ceiling: 67108864,
      max_retention_days: 3650,
      max_stored_requests_limit: 10000000,
    },
    server: {
      version: "0.1.0",
      commit: "unknown",
      build_date: "unknown",
      started_at: "2026-10-10T09:00:00Z",
      listen_address: "127.0.0.1:8787",
      public_base_url: "http://127.0.0.1:8787",
      database_path: "./data/relaybox.db",
      database_size_bytes: 65536,
      schema_version: 1,
      log_level: "info",
      log_format: "text",
      remote_dashboard: false,
      auth_required: false,
      url_replay_enabled: true,
      replay_allowed_hosts: [],
      rate_limit_rps: 50,
      rate_limit_burst: 100,
      max_body_bytes_limit: 67108864,
      replay_timeout_ms: 30000,
      replay_response_bytes: 1048576,
    },
  };
}

function section(name: string) {
  return screen.getByRole("heading", { level: 2, name }).closest("section")!;
}

describe("Settings page", () => {
  it("saves retention changes and marks which settings need a restart", async () => {
    let current = settings();
    const { calls } = mockApi({
      "GET /api/v1/settings": () => ({ body: current }),
      "PATCH /api/v1/settings": (req) => {
        current = settings(req.body as SettingsPatch, ["retention_days"]);
        return { body: current };
      },
    });
    const user = userEvent.setup();
    renderApp("/settings");

    const days = await screen.findByLabelText("Keep requests for (days)");
    expect(within(section("Retention")).getByText("Applies immediately")).toBeInTheDocument();
    expect(within(section("Replay security")).getByText("Restart required")).toBeInTheDocument();
    expect(within(section("Server")).getByText("0.1.0")).toBeInTheDocument();

    await user.clear(days);
    await user.type(days, "30");
    await user.click(within(section("Retention")).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    expect(calls.find((c) => c.method === "PATCH")!.body).toEqual({
      retention_days: 30,
      max_stored_requests: 10000,
    });
    expect(await screen.findByText("Settings saved.")).toBeInTheDocument();
    expect(within(section("Retention")).getByRole("button", { name: "Reset" })).toBeInTheDocument();
  });

  it("shows a rejected value next to its field", async () => {
    mockApi({
      "GET /api/v1/settings": { body: settings() },
      "PATCH /api/v1/settings": {
        status: 422,
        body: {
          error: {
            code: "validation_failed",
            message: "Some fields are invalid.",
            fields: { retention_days: "Must be between 0 and 3650." },
          },
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/settings");

    const days = await screen.findByLabelText("Keep requests for (days)");
    await user.clear(days);
    await user.type(days, "9999");
    await user.click(within(section("Retention")).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Must be between 0 and 3650.")).toBeInTheDocument();
  });

  it("saves redaction lists one name per line", async () => {
    const { calls } = mockApi({
      "GET /api/v1/settings": { body: settings() },
      "PATCH /api/v1/settings": (req) => ({ body: settings(req.body as SettingsPatch) }),
    });
    const user = userEvent.setup();
    renderApp("/settings");

    const headers = await screen.findByLabelText("Header names");
    expect(headers).toHaveValue("Authorization\nCookie");
    await user.type(headers, "\nX-Signature\n\n");
    await user.click(within(section("Redaction")).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({
        redact_headers: ["Authorization", "Cookie", "X-Signature"],
        redact_fields: ["password", "token"],
      }),
    );
  });

  it("deletes everything only after confirmation", async () => {
    const { calls } = mockApi({
      "GET /api/v1/settings": { body: settings() },
      "GET /api/v1/stats": { body: stats({ total_requests: 12 }) },
      "DELETE /api/v1/requests": { body: { deleted: 12 } },
    });
    const user = userEvent.setup();
    renderApp("/settings");

    await screen.findByRole("heading", { level: 2, name: "Export and delete" });
    await user.click(
      await within(section("Export and delete")).findByRole("button", { name: "Delete 12 requests" }),
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    const dialog = screen.getByRole("dialog", { name: "Delete every stored request?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete 12 requests" }));

    expect(await screen.findByText("Deleted 12 requests.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "DELETE")!.url.searchParams.get("scope")).toBe("all");
  });
});
