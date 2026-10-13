import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { body, endpoint } from "../test/fixtures";
import { mockApi, renderApp } from "../test/utils";
import type { MockResponse } from "../types/api";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const ep = endpoint({ id: 2, name: "Orders", request_count: 3 });

describe("Endpoint detail page", () => {
  it("saves an edited mock response", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "PATCH /api/v1/endpoints/2": (req) => ({
        body: { ...ep, mock_response: (req.body as { mock_response: MockResponse }).mock_response },
      }),
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    const status = await screen.findByLabelText("Status");
    expect(screen.getByRole("button", { name: "Save response" })).toBeDisabled();
    await user.clear(status);
    await user.type(status, "202");
    await user.click(screen.getByRole("button", { name: "Add header" }));
    await user.type(screen.getByLabelText("Header name"), "X-Trace");
    await user.type(screen.getByLabelText("Value for X-Trace"), "abc");
    await user.click(screen.getByRole("button", { name: "Save response" }));

    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    expect(calls.find((c) => c.method === "PATCH")!.body).toEqual({
      mock_response: {
        status: 202,
        content_type: "application/json",
        headers: { "X-Trace": "abc" },
        body: '{"received":true}',
        delay_ms: 0,
      },
    });
    expect(await screen.findByText("Mock response saved.")).toBeInTheDocument();
  });

  it("shows field errors from the server next to the field", async () => {
    mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "PATCH /api/v1/endpoints/2": {
        status: 422,
        body: {
          error: {
            code: "validation_failed",
            message: "Some fields are invalid.",
            fields: { "mock_response.body": "The content type is JSON, but the body is not valid JSON." },
          },
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    const bodyField = await screen.findByLabelText("Body");
    await user.clear(bodyField);
    await user.type(bodyField, "not json");
    await user.click(screen.getByRole("button", { name: "Save response" }));

    expect(
      await screen.findByText("The content type is JSON, but the body is not valid JSON."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Body")).toHaveAttribute("aria-invalid", "true");
  });

  it("previews the response a sender would get", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "POST /api/v1/mock-preview": {
        body: {
          status: 200,
          status_text: "OK",
          headers: [["Content-Type", "application/json"]],
          body: body({ data: '{"received":true}' }),
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    await user.click(await screen.findByRole("button", { name: "Preview" }));
    expect(await screen.findByText("A sender would receive")).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/v1/mock-preview")?.body).toMatchObject({
      method: "POST",
      mock_response: { status: 200 },
    });
  });

  it("imports a fixture file into the endpoint", async () => {
    const fixture = { format: "relaybox.fixture", version: 1, requests: [] };
    const { calls } = mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "POST /api/v1/fixtures/import": { status: 201, body: { imported: 2, endpoint_id: 2 } },
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    const file = new File([JSON.stringify(fixture)], "fixture.json", { type: "application/json" });
    await user.upload(await screen.findByLabelText("Fixture file"), file);

    expect(await screen.findByText("Imported 2 requests into Orders.")).toBeInTheDocument();
    const call = calls.find((c) => c.path === "/api/v1/fixtures/import")!;
    expect(call.url.searchParams.get("endpoint_id")).toBe("2");
    expect(call.body).toEqual(fixture);
  });

  it("lists every problem when a fixture is rejected", async () => {
    mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "POST /api/v1/fixtures/import": {
        status: 422,
        body: {
          error: {
            code: "invalid_fixture",
            message: "The fixture is not valid.",
            details: ["requests[0].method: TRACE is not supported", "requests[1].body: invalid base64"],
          },
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    await user.upload(await screen.findByLabelText("Fixture file"), new File(["{}"], "bad.json"));

    expect(await screen.findByText("The fixture was not imported")).toBeInTheDocument();
    expect(screen.getByText("requests[0].method: TRACE is not supported")).toBeInTheDocument();
    expect(screen.getByText("requests[1].body: invalid base64")).toBeInTheDocument();
  });

  it("downloads a fixture export", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "POST /api/v1/fixtures/export": {
        body: { format: "relaybox.fixture", version: 1, requests: [] },
        headers: { "Content-Disposition": 'attachment; filename="relaybox-orders.json"' },
      },
    });
    const createURL = vi.fn(() => "blob:fixture");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: createURL, revokeObjectURL: vi.fn() }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      expect(this.download).toBe("relaybox-orders.json");
      expect(this.href).toBe("blob:fixture");
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    await user.click(await screen.findByRole("button", { name: "Export fixture" }));

    await waitFor(() => expect(click).toHaveBeenCalled());
    expect(createURL).toHaveBeenCalled();
    expect(calls.find((c) => c.path === "/api/v1/fixtures/export")?.body).toEqual({
      endpoint_id: 2,
      limit: 100,
      include_bodies: true,
    });
  });

  it("asks before deleting the endpoint", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints/2": { body: ep },
      "DELETE /api/v1/endpoints/2": { body: { deleted_requests: 3 } },
    });
    const user = userEvent.setup();
    renderApp("/endpoints/2");

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    const dialog = screen.getByRole("dialog", { name: "Delete Orders?" });
    expect(dialog).toHaveTextContent("3 captured requests are deleted with it");
    await user.click(within(dialog).getByRole("button", { name: "Delete endpoint" }));

    expect(await screen.findByText("Deleted Orders and 3 requests.")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { level: 1, name: "Endpoints" })).toBeInTheDocument();
  });
});
