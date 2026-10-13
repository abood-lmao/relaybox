import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { endpoint } from "../test/fixtures";
import { mockApi, renderApp } from "../test/utils";

afterEach(() => vi.unstubAllGlobals());

describe("Endpoints page", () => {
  it("creates an endpoint and opens it", async () => {
    const created = endpoint({ id: 7, name: "Payments staging" });
    const { calls } = mockApi({
      "POST /api/v1/endpoints": { status: 201, body: created },
      "GET /api/v1/endpoints/7": { body: created },
    });
    const user = userEvent.setup();
    renderApp("/endpoints");

    await user.click(await screen.findByRole("button", { name: "Create your first endpoint" }));
    await user.type(screen.getByLabelText("Name"), "Payments staging");
    await user.click(screen.getByRole("button", { name: "Create endpoint" }));

    expect(await screen.findByRole("heading", { level: 1, name: /Payments staging/ })).toBeInTheDocument();
    expect(screen.getByText("Endpoint created")).toBeInTheDocument();
    expect(screen.getAllByText(created.url).length).toBeGreaterThan(0);
    expect(calls.find((c) => c.method === "POST" && c.path === "/api/v1/endpoints")?.body).toEqual({
      name: "Payments staging",
    });
  });

  it("shows the server's validation message", async () => {
    mockApi({
      "POST /api/v1/endpoints": {
        status: 422,
        body: {
          error: {
            code: "validation_failed",
            message: "Some fields are invalid.",
            fields: { name: "Another endpoint already uses this name." },
          },
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/endpoints?new=1");

    await user.type(await screen.findByLabelText("Name"), "Duplicate");
    await user.click(screen.getByRole("button", { name: "Create endpoint" }));

    expect(await screen.findByText("Another endpoint already uses this name.")).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toHaveAttribute("aria-invalid", "true");
  });

  it("lists endpoints and toggles one off", async () => {
    const ep = endpoint({ id: 3, name: "GitHub app", request_count: 12 });
    const { calls } = mockApi({
      "GET /api/v1/endpoints": { body: { items: [ep] } },
      "PATCH /api/v1/endpoints/3": { body: { ...ep, enabled: false } },
    });
    const user = userEvent.setup();
    renderApp("/endpoints");

    const row = (await screen.findByRole("link", { name: "GitHub app" })).closest("tr")!;
    expect(within(row).getByText(ep.url)).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "12" })).toHaveAttribute("href", "/requests?endpoint_id=3");

    await user.click(within(row).getByRole("switch", { name: "Accept requests for GitHub app" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ enabled: false }));
    expect(await screen.findByText("GitHub app is disabled.")).toBeInTheDocument();
  });

  it("shows an error state with retry when loading fails", async () => {
    let fail = true;
    mockApi({
      "GET /api/v1/endpoints": () =>
        fail
          ? {
              status: 500,
              body: { error: { code: "internal_error", message: "Something went wrong on the server." } },
            }
          : { body: { items: [endpoint({ name: "Recovered" })] } },
    });
    const user = userEvent.setup();
    renderApp("/endpoints");

    expect(await screen.findByText("Something went wrong on the server.")).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("link", { name: "Recovered" })).toBeInTheDocument();
  });
});
