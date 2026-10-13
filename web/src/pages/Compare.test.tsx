import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { summary } from "../test/fixtures";
import { mockApi, renderApp, type MockRequest } from "../test/utils";
import type { CompareBody, Comparison } from "../types/api";

afterEach(() => vi.unstubAllGlobals());

function comparison(body: Partial<CompareBody>, over: Partial<Comparison> = {}): Comparison {
  return {
    part: "request",
    left: summary({ id: 1, path: "/left" }),
    right: summary({ id: 2, path: "/right" }),
    headers: [],
    body: {
      mode: "json",
      equal: false,
      differences: [],
      lines: null,
      truncated: false,
      approximate: false,
      incomplete_data: false,
      ...body,
    },
    ...over,
  };
}

describe("Compare page", () => {
  it("lists JSON differences by path, with redaction marks", async () => {
    const { calls } = mockApi({
      "POST /api/v1/requests/compare": {
        body: comparison(
          {
            differences: [
              { path: "$.amount", kind: "changed", left: "1000", right: "1250" },
              { path: "$.card.number", kind: "changed", left: '"[REDACTED]"', right: '"4242"' },
              { path: "$.coupon", kind: "added", right: '"SPRING"' },
            ],
          },
          { headers: [{ path: "X-Attempt", kind: "changed", left: "1", right: "2" }] },
        ),
      },
    });
    renderApp("/compare?left=1&right=2");

    expect(await screen.findByText("$.amount")).toBeInTheDocument();
    expect(screen.getByText("1250")).toBeInTheDocument();
    expect(screen.getByText("$.coupon")).toBeInTheDocument();
    expect(screen.getByText("X-Attempt")).toBeInTheDocument();
    expect(document.querySelector("td .redacted")).toHaveTextContent("[REDACTED]");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      left_id: 1,
      right_id: 2,
      part: "request",
    });
  });

  it("shows a line diff for text bodies and can swap sides", async () => {
    const posts: MockRequest[] = [];
    mockApi({
      "POST /api/v1/requests/compare": (req) => {
        posts.push(req);
        return {
          body: comparison({
            mode: "text",
            differences: null,
            lines: [
              { op: "equal", text: "a=1", left: 1, right: 1 },
              { op: "delete", text: "b=2", left: 2 },
              { op: "insert", text: "b=3", right: 2 },
            ],
          }),
        };
      },
    });
    const user = userEvent.setup();
    renderApp("/compare?left=1&right=2");

    expect(await screen.findByText("b=3")).toBeInTheDocument();
    expect(screen.getByLabelText("added")).toBeInTheDocument();
    expect(screen.getByLabelText("removed")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Swap left and right" }));
    await waitFor(() => expect(posts.at(-1)?.body).toEqual({ left_id: 2, right_id: 1, part: "request" }));
  });

  it("compares the responses when asked", async () => {
    const { calls } = mockApi({
      "POST /api/v1/requests/compare": {
        body: comparison({ equal: true, mode: "json" }, { part: "response" }),
      },
    });
    const user = userEvent.setup();
    renderApp("/compare?left=1&right=2");

    await screen.findByText("The headers are identical.");
    await user.click(screen.getByRole("tab", { name: "Response" }));
    await waitFor(() => expect(calls.at(-1)?.body).toEqual({ left_id: 1, right_id: 2, part: "response" }));
  });

  it("asks for two different requests", async () => {
    mockApi({});
    renderApp("/compare?left=3&right=3");
    expect(await screen.findByText("Choose two different requests")).toBeInTheDocument();
  });
});
