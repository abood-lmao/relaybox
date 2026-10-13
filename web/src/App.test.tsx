import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { endpoint, meta, stats, summary } from "./test/fixtures";
import { mockApi, renderApp } from "./test/utils";

afterEach(() => vi.unstubAllGlobals());

describe("App", () => {
  it("asks for the access token when the server requires one", async () => {
    let signedIn = false;
    const { calls } = mockApi({
      "GET /api/v1/auth/session": () => ({ body: { auth_required: true, authenticated: signedIn } }),
      "POST /api/v1/auth/login": (req) => {
        if ((req.body as { token: string }).token !== "a-long-enough-dashboard-token") {
          return {
            status: 401,
            body: { error: { code: "unauthorized", message: "That token is not valid." } },
          };
        }
        signedIn = true;
        return { body: { auth_required: true, authenticated: true } };
      },
      "GET /api/v1/meta": { body: { ...meta, auth_required: true, remote_dashboard: true } },
    });
    const user = userEvent.setup();
    renderApp("/");

    const token = await screen.findByLabelText("Access token");
    await user.type(token, "wrong");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("That token is not valid.")).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/api/v1/stats")).toBe(false);

    await user.clear(token);
    await user.type(token, "a-long-enough-dashboard-token");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign out" })).toBeInTheDocument();
  });

  it("returns to sign in when the session expires", async () => {
    mockApi({
      "GET /api/v1/auth/session": { body: { auth_required: true, authenticated: true } },
      "GET /api/v1/meta": { body: { ...meta, auth_required: true } },
      "GET /api/v1/stats": {
        status: 401,
        body: { error: { code: "unauthorized", message: "Sign in to continue." } },
      },
    });
    renderApp("/");
    expect(await screen.findByLabelText("Access token")).toBeInTheDocument();
  });

  it("explains when the server cannot be reached", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );
    renderApp("/");
    expect(await screen.findByText("Relaybox is not reachable", {}, { timeout: 4000 })).toBeInTheDocument();
    expect(
      screen.getByText("Relaybox did not respond. Check that the server is running."),
    ).toBeInTheDocument();
  });

  it("walks a fresh install through creating an endpoint", async () => {
    const ep = endpoint({ id: 1, name: "My first endpoint" });
    let created = false;
    const { calls } = mockApi({
      "GET /api/v1/endpoints": () => ({ body: { items: created ? [ep] : [] } }),
      "POST /api/v1/endpoints": () => {
        created = true;
        return { status: 201, body: ep };
      },
    });
    const user = userEvent.setup();
    renderApp("/");

    expect(await screen.findByText("Get started")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Endpoint name"), "My first endpoint");
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "My first endpoint" }),
    );
    expect(await screen.findByRole("link", { name: "My first endpoint" })).toHaveAttribute(
      "href",
      "/endpoints/1",
    );
    expect(screen.getAllByText(new RegExp(ep.url)).length).toBeGreaterThan(0);
  });

  it("summarizes traffic on the overview", async () => {
    mockApi({
      "GET /api/v1/endpoints": { body: { items: [endpoint({ request_count: 3 })] } },
      "GET /api/v1/stats": {
        body: stats({
          total_requests: 3,
          last_24h: 3,
          endpoints: 1,
          status_classes: { "2xx": 2, "3xx": 0, "4xx": 0, "5xx": 1 },
          top_endpoints: [
            {
              id: 1,
              name: "Endpoint 1",
              enabled: true,
              last_24h: 3,
              total: 3,
              last_request_at: "2026-10-10T10:05:00Z",
            },
          ],
          recent: [summary({ id: 3, path: "/latest", status: 500 }), summary({ id: 2 }), summary({ id: 1 })],
        }),
      },
    });
    renderApp("/");

    expect(await screen.findByRole("link", { name: "/latest" })).toHaveAttribute("href", "/requests/3");
    expect(screen.getByText("Stored requests").parentElement).toHaveTextContent("3");
    expect(screen.queryByText("Get started")).toBeNull();
  });

  it("switches theme and remembers the choice", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderApp("/");

    await user.click(await screen.findByRole("radio", { name: "Dark" }));
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem("relaybox-theme")).toBe("dark");
    await user.click(screen.getByRole("radio", { name: "Light" }));
    expect(document.documentElement).not.toHaveClass("dark");
  });

  it("shows a not found page for unknown addresses", async () => {
    mockApi({});
    renderApp("/nope");
    expect(await screen.findByText("Page not found")).toBeInTheDocument();
  });
});
