import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { body, detail } from "../test/fixtures";
import { mockApi, renderApp } from "../test/utils";

afterEach(() => {
  vi.unstubAllGlobals();
  delete (window as { __xss?: number }).__xss;
});

describe("Request detail page", () => {
  it("never interprets captured markup", async () => {
    const markup = `<img src=x onerror="window.__xss=1"><script>window.__xss=2</script>`;
    mockApi({
      "GET /api/v1/requests/5": {
        body: detail({
          id: 5,
          path: `/render/<b>bold</b>`,
          headers: [["X-Note", `<svg onload="window.__xss=3">`]],
          body: body({ data: markup, kind: "text", content_type: "text/html" }),
        }),
      },
    });
    const user = userEvent.setup();
    const { container } = renderApp("/requests/5");

    await screen.findByRole("heading", { level: 1 });
    await user.click(screen.getByRole("tab", { name: "Body" }));
    expect(await screen.findByText(markup)).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: /Headers/ }));
    expect(screen.getByText(`<svg onload="window.__xss=3">`)).toBeInTheDocument();

    expect(container.querySelector("img, script, svg[onload], b")).toBeNull();
    expect((window as { __xss?: number }).__xss).toBeUndefined();
  });

  it("marks redacted values and lists what was redacted", async () => {
    mockApi({
      "GET /api/v1/requests/6": {
        body: detail({
          id: 6,
          redacted: true,
          headers: [
            ["Authorization", "Bearer [REDACTED]"],
            ["Content-Type", "application/json"],
          ],
          body: body({ data: '{"password":"[REDACTED]","user":"ada"}' }),
          redactions: [
            { location: "header", name: "Authorization" },
            { location: "body", name: "$.password" },
          ],
        }),
      },
    });
    const user = userEvent.setup();
    renderApp("/requests/6");

    expect(await screen.findByText("2 values redacted before storage")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: /Headers/ }));
    const row = screen.getByRole("rowheader", { name: "Authorization" }).closest("tr")!;
    expect(row.querySelector(".redacted")).toHaveTextContent("[REDACTED]");
    expect(within(row).queryByRole("button", { name: /Copy/ })).toBeNull();

    await user.click(screen.getByRole("tab", { name: "Body" }));
    expect(document.querySelector(".redacted")).toHaveTextContent("[REDACTED]");
  });

  it("shows JSON numbers exactly as received", async () => {
    mockApi({
      "GET /api/v1/requests/7": {
        body: detail({ id: 7, body: body({ data: '{"ledger_id":9007199254740993,"price":1.10}' }) }),
      },
    });
    const user = userEvent.setup();
    renderApp("/requests/7?tab=body");

    expect(await screen.findByText("9007199254740993")).toBeInTheDocument();
    expect(screen.getByText("1.10")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Formatted" }));
    expect(document.querySelector("pre")).toHaveTextContent('"ledger_id": 9007199254740993');
  });

  it("shows binary bodies as a hex dump with a download link", async () => {
    mockApi({
      "GET /api/v1/requests/8": {
        body: detail({
          id: 8,
          body: body({
            encoding: "base64",
            data: "iVBORw0KGgo=",
            kind: "binary",
            content_type: "image/png",
            size: 8,
            stored_bytes: 8,
          }),
        }),
      },
    });
    renderApp("/requests/8?tab=body");

    expect(await screen.findByText(/89 50 4e 47 0d 0a 1a 0a/)).toBeInTheDocument();
    expect(screen.getByText(/Binary content is never rendered/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Download" })).toHaveAttribute(
      "href",
      "/api/v1/requests/8/body?part=request",
    );
  });

  it("warns when the body was truncated", async () => {
    mockApi({
      "GET /api/v1/requests/9": {
        body: detail({
          id: 9,
          body_truncated: true,
          body: body({
            truncated: true,
            size: 4096,
            stored_bytes: 1024,
            data: "x".repeat(1024),
            kind: "text",
          }),
        }),
      },
    });
    renderApp("/requests/9?tab=body");
    expect(await screen.findByText("Body truncated")).toBeInTheDocument();
    expect(screen.getByText(/Only the first 1 KiB of 4 KiB were stored/)).toBeInTheDocument();
  });

  it("shows the response that was sent back", async () => {
    mockApi({
      "GET /api/v1/requests/10": {
        body: detail({
          id: 10,
          status: 503,
          response: {
            status: 503,
            status_text: "Service Unavailable",
            headers: [["Retry-After", "30"]],
            body: body({ data: '{"error":"later"}' }),
          },
        }),
      },
    });
    renderApp("/requests/10?tab=response");
    expect(await screen.findByText("Retry-After")).toBeInTheDocument();
    expect(screen.getAllByText("Service Unavailable").length).toBeGreaterThan(0);
  });

  it("explains a missing request", async () => {
    mockApi({
      "GET /api/v1/requests/404": {
        status: 404,
        body: { error: { code: "not_found", message: "Request not found." } },
      },
    });
    renderApp("/requests/404");
    expect(await screen.findByText("Request not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to requests" })).toBeInTheDocument();
  });
});
