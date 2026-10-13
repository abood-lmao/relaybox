import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { endpoint, summary } from "../test/fixtures";
import { mockApi, renderApp } from "../test/utils";
import type { RequestSummary } from "../types/api";

afterEach(() => vi.unstubAllGlobals());

const rows: RequestSummary[] = [
  summary({ id: 1, method: "POST", path: "/orders", status: 200 }),
  summary({
    id: 2,
    method: "PUT",
    path: "/orders/7",
    status: 500,
    query_string: "token=%5BREDACTED%5D",
    redacted: true,
  }),
];

function page(items: RequestSummary[], total = items.length) {
  return { body: { items, total, limit: 50, offset: 0 } };
}

describe("Requests page", () => {
  it("lists requests with method, status, and redaction marks", async () => {
    mockApi({
      "GET /api/v1/endpoints": { body: { items: [endpoint({ request_count: 2 })] } },
      "GET /api/v1/requests": page(rows),
    });
    renderApp("/requests");

    const link = await screen.findByRole("link", { name: "/orders/7?token=[REDACTED]" });
    const row = link.closest("tr")!;
    expect(within(row).getByText("PUT")).toBeInTheDocument();
    expect(within(row).getByText("500")).toBeInTheDocument();
    expect(row.querySelectorAll(".redacted").length).toBeGreaterThan(0);
    expect(link).toHaveAttribute("href", "/requests/2");
    expect(screen.getByText("1–2 of 2")).toBeInTheDocument();
  });

  it("sends filters to the API and keeps them in the address", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints": { body: { items: [endpoint({ request_count: 2 })] } },
      "GET /api/v1/requests": (req) =>
        page(req.url.searchParams.get("method") === "PUT" ? rows.filter((r) => r.method === "PUT") : rows),
    });
    const user = userEvent.setup();
    renderApp("/requests");
    await screen.findByRole("link", { name: "/orders" });

    await user.selectOptions(screen.getByLabelText("Method"), "PUT");
    await user.selectOptions(screen.getByLabelText("Status"), "5xx");

    await waitFor(() => expect(screen.queryByRole("link", { name: "/orders" })).not.toBeInTheDocument());
    const last = calls.filter((c) => c.path === "/api/v1/requests").at(-1)!;
    expect(last.url.searchParams.get("method")).toBe("PUT");
    expect(last.url.searchParams.get("status")).toBe("5xx");
    expect(last.url.searchParams.get("sort")).toBe("received_at");
    expect(last.url.searchParams.get("order")).toBe("desc");
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeInTheDocument();
  });

  it("searches from the top bar", async () => {
    const { calls } = mockApi({ "GET /api/v1/requests": page(rows) });
    const user = userEvent.setup();
    renderApp("/requests");
    await screen.findByRole("link", { name: "/orders" });

    await user.type(screen.getByRole("searchbox", { name: "Search requests" }), "ord_1{Enter}");

    await waitFor(() =>
      expect(
        calls.some((c) => c.path === "/api/v1/requests" && c.url.searchParams.get("q") === "ord_1"),
      ).toBe(true),
    );
    expect(screen.getByText(/whose path, query, headers, or body contain/)).toBeInTheDocument();
  });

  it("explains an empty result when filters match nothing", async () => {
    mockApi({ "GET /api/v1/requests": page([]) });
    renderApp("/requests?status=4xx");
    expect(await screen.findByText("No requests match these filters")).toBeInTheDocument();
  });

  it("guides new users when there are no endpoints", async () => {
    mockApi({});
    renderApp("/requests");
    expect(await screen.findByText("No requests yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Create an endpoint" })).toHaveAttribute(
      "href",
      "/endpoints?new=1",
    );
  });

  it("deletes only the selected requests after confirmation", async () => {
    const { calls } = mockApi({
      "GET /api/v1/endpoints": { body: { items: [endpoint({ request_count: 2 })] } },
      "GET /api/v1/requests": page(rows),
      "DELETE /api/v1/requests": { body: { deleted: 1 } },
    });
    const user = userEvent.setup();
    renderApp("/requests");

    await user.click(await screen.findByRole("checkbox", { name: "Select request 2" }));
    expect(screen.getByText("1 selected")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    const dialog = screen.getByRole("dialog", { name: "Delete 1 request?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    const del = calls.find((c) => c.method === "DELETE")!;
    expect(del.url.searchParams.get("scope")).toBe("ids");
    expect(del.url.searchParams.get("ids")).toBe("2");
    expect(await screen.findByText("Deleted 1 request.")).toBeInTheDocument();
  });

  it("opens the comparison for exactly two selected requests", async () => {
    mockApi({
      "GET /api/v1/requests": page(rows),
      "POST /api/v1/requests/compare": {
        body: {
          part: "request",
          left: rows[0],
          right: rows[1],
          headers: [],
          body: {
            mode: "json",
            equal: false,
            differences: [{ path: "$.amount", kind: "changed", left: "10", right: "12" }],
            lines: null,
            truncated: false,
            approximate: false,
            incomplete_data: false,
          },
        },
      },
    });
    const user = userEvent.setup();
    renderApp("/requests");

    await screen.findByRole("link", { name: "/orders" });
    expect(screen.queryByRole("button", { name: "Compare" })).toBeNull();
    await user.click(screen.getByRole("checkbox", { name: "Select request 1" }));
    expect(screen.getByRole("button", { name: "Compare" })).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "Select request 2" }));
    await user.click(screen.getByRole("button", { name: "Compare" }));

    expect(await screen.findByText("$.amount")).toBeInTheDocument();
    expect(screen.getByText("The headers are identical.")).toBeInTheDocument();
  });
});
