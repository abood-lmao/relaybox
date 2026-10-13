import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { body, endpoint, summary } from "../test/fixtures";
import { mockApi, renderApp } from "../test/utils";
import type { ReplayAttempt, ReplayDraft, ReplayInput } from "../types/api";

afterEach(() => vi.unstubAllGlobals());

function draft(over: Partial<ReplayDraft> = {}): ReplayDraft {
  return {
    request: summary({ id: 4 }),
    method: "POST",
    headers: [
      { name: "Authorization", value: "Bearer [REDACTED]", selected: false, redacted: true },
      { name: "Content-Type", value: "application/json", selected: true, redacted: false },
      {
        name: "Host",
        value: "127.0.0.1:8787",
        selected: false,
        redacted: false,
        blocked: "set from the destination",
      },
    ],
    body: body(),
    url_replay_enabled: true,
    allowed_hosts: [],
    max_body_bytes: 10485760,
    ...over,
  };
}

function attempt(over: Partial<ReplayAttempt> = {}): ReplayAttempt {
  return {
    id: 1,
    request_id: 4,
    target: "url",
    destination: "https://example.com/hook",
    destination_host: "example.com",
    target_endpoint_id: null,
    result_request_id: null,
    method: "POST",
    response_status: 204,
    response_size: 0,
    duration_ms: 42,
    outcome: "success",
    summary: "Delivered, received 204 No Content.",
    created_at: "2026-10-10T10:10:00Z",
    ...over,
  };
}

const endpoints = [endpoint({ id: 1, name: "Stripe test" }), endpoint({ id: 2, name: "Local copy" })];

describe("Replay page", () => {
  it("sends nothing until the replay is confirmed", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints": { body: { items: endpoints } },
      "GET /api/v1/requests/4/replay": { body: draft() },
      "GET /api/v1/requests/4/replays": { body: { items: [] } },
      "POST /api/v1/requests/4/replay": {
        body: {
          attempt: attempt(),
          response: {
            status: 204,
            status_text: "No Content",
            headers: [],
            body: body({ data: "", kind: "empty", size: 0, stored_bytes: 0 }),
          },
          created_request_id: null,
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/requests/4/replay");

    expect(await screen.findByRole("checkbox", { name: "Forward Authorization" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Forward Content-Type" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Forward Host" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Review and send" })).toBeDisabled();

    await user.type(screen.getByLabelText("Destination URL"), "https://example.com/hook");
    await user.click(screen.getByRole("button", { name: "Review and send" }));

    const dialog = screen.getByRole("dialog", { name: "Send to example.com?" });
    expect(dialog).toHaveTextContent("This sends a real HTTP request to https://example.com/hook");
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.click(within(dialog).getByRole("button", { name: "Send request" }));

    expect(await screen.findByText("Replay delivered")).toBeInTheDocument();
    const sent = calls.find((c) => c.method === "POST")!.body as ReplayInput;
    expect(sent).toEqual({
      target: "url",
      url: "https://example.com/hook",
      method: "POST",
      headers: [["Content-Type", "application/json"]],
      confirmed: true,
    });
  });

  it("cancelling the review sends nothing", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints": { body: { items: endpoints } },
      "GET /api/v1/requests/4/replay": { body: draft() },
      "GET /api/v1/requests/4/replays": { body: { items: [] } },
    });
    const user = userEvent.setup();
    renderApp("/requests/4/replay");

    await user.type(await screen.findByLabelText("Destination URL"), "https://example.com/hook");
    await user.click(screen.getByRole("button", { name: "Review and send" }));
    await user.click(
      within(screen.getByRole("dialog", { name: "Send to example.com?" })).getByRole("button", {
        name: "Cancel",
      }),
    );

    expect(screen.queryByRole("dialog", { name: "Send to example.com?" })).toBeNull();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("replays into an endpoint with an edited body and a typed header value", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints": { body: { items: endpoints } },
      "GET /api/v1/requests/4/replay": { body: draft() },
      "GET /api/v1/requests/4/replays": { body: { items: [] } },
      "POST /api/v1/requests/4/replay": {
        status: 201,
        body: {
          attempt: attempt({
            target: "endpoint",
            target_endpoint_id: 2,
            result_request_id: 9,
            destination: "endpoint:2",
            destination_host: "",
          }),
          response: null,
          created_request_id: 9,
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/requests/4/replay");

    await user.click(await screen.findByRole("radio", { name: /A Relaybox endpoint/ }));
    expect(within(screen.getByRole("main")).getByLabelText("Endpoint")).toHaveValue("2");

    const auth = screen.getByLabelText("Value for Authorization");
    await user.clear(auth);
    await user.type(auth, "Bearer test-token");
    expect(screen.getByRole("checkbox", { name: "Forward Authorization" })).toBeChecked();

    await user.click(screen.getByRole("button", { name: "Edit body" }));
    const edited = screen.getByLabelText("Body to send");
    await user.clear(edited);
    await user.type(edited, "{{}");

    await user.click(screen.getByRole("button", { name: "Review and send" }));
    await user.click(
      within(screen.getByRole("dialog", { name: "Replay into Local copy?" })).getByRole("button", {
        name: "Replay",
      }),
    );

    expect(await screen.findByRole("link", { name: "Open request #9" })).toHaveAttribute(
      "href",
      "/requests/9",
    );
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({
      target: "endpoint",
      endpoint_id: 2,
      method: "POST",
      headers: [
        ["Authorization", "Bearer test-token"],
        ["Content-Type", "application/json"],
      ],
      confirmed: false,
      body: { encoding: "utf8", data: "{}" },
    });
  });

  it("requires a decision before sending a truncated body", async () => {
    mockApi({
      "GET /api/v1/endpoints": { body: { items: endpoints } },
      "GET /api/v1/requests/4/replay": {
        body: draft({
          body: body({
            truncated: true,
            size: 4096,
            stored_bytes: 1024,
            data: "x".repeat(1024),
            kind: "text",
          }),
        }),
      },
      "GET /api/v1/requests/4/replays": { body: { items: [] } },
    });
    const user = userEvent.setup();
    renderApp("/requests/4/replay");

    await user.type(await screen.findByLabelText("Destination URL"), "https://example.com/hook");
    expect(screen.getByRole("button", { name: "Review and send" })).toBeDisabled();
    expect(screen.getByText("Decide how to handle the incomplete body.")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Send the stored part anyway" }));
    expect(screen.getByRole("button", { name: "Review and send" })).toBeEnabled();
  });

  it("disables URL replay when the server does not allow it", async () => {
    mockApi({
      "GET /api/v1/endpoints": { body: { items: endpoints } },
      "GET /api/v1/requests/4/replay": { body: draft({ url_replay_enabled: false }) },
      "GET /api/v1/requests/4/replays": {
        body: {
          items: [attempt({ outcome: "failure", summary: "Blocked: 10.0.0.5 is a private address." })],
        },
      },
    });
    renderApp("/requests/4/replay");

    expect(await screen.findByRole("radio", { name: /A URL/ })).toBeDisabled();
    expect(screen.getByRole("radio", { name: /A Relaybox endpoint/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByText(/RELAYBOX_ALLOW_REMOTE_REPLAY=true/)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText("Blocked: 10.0.0.5 is a private address.")).toBeInTheDocument(),
    );
  });

  it("shows the server's reason when a replay is refused", async () => {
    mockApi({
      "GET /api/v1/endpoints": { body: { items: endpoints } },
      "GET /api/v1/requests/4/replay": { body: draft() },
      "GET /api/v1/requests/4/replays": { body: { items: [] } },
      "POST /api/v1/requests/4/replay": {
        status: 422,
        body: {
          error: {
            code: "validation_failed",
            message: "Some fields are invalid.",
            fields: { url: "That address is private or loopback, so it is blocked." },
          },
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/requests/4/replay");

    await user.type(await screen.findByLabelText("Destination URL"), "http://192.168.1.10/hook");
    await user.click(screen.getByRole("button", { name: "Review and send" }));
    await user.click(screen.getByRole("button", { name: "Send request" }));

    expect(
      await screen.findByText("That address is private or loopback, so it is blocked."),
    ).toBeInTheDocument();
  });
});
