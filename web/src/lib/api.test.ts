import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, errorMessage } from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("api client", () => {
  it("turns API error bodies into ApiError with fields and details", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: {
                code: "validation_failed",
                message: "Check the fields.",
                fields: { name: "Enter a name." },
              },
            }),
            { status: 422, headers: { "Content-Type": "application/json" } },
          ),
      ),
    );
    const err = await api.createEndpoint({ name: "" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 422, code: "validation_failed", fields: { name: "Enter a name." } });
    expect(errorMessage(err)).toBe("Check the fields.");
  });

  it("reports network failures as status 0", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );
    const err = (await api.meta().catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(0);
    expect(err.code).toBe("network_error");
  });

  it("handles non-JSON error responses", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("gateway down", { status: 502, statusText: "Bad Gateway" })),
    );
    const err = (await api.stats().catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(502);
    expect(err.message).toContain("502");
  });

  it("builds request list queries without empty values", async () => {
    const fetchMock = vi.fn(
      async () => new Response(JSON.stringify({ items: [], total: 0, limit: 50, offset: 0 })),
    );
    vi.stubGlobal("fetch", fetchMock);
    await api.listRequests({ method: "POST", status: "", q: "ord 1", limit: "50" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/requests?method=POST&q=ord+1&limit=50",
      expect.anything(),
    );
  });

  it("sends JSON with a content type and same-origin credentials", async () => {
    const fetchMock = vi.fn(async (..._args: unknown[]) => new Response(JSON.stringify({ deleted: 2 })));
    vi.stubGlobal("fetch", fetchMock);
    await api.deleteRequests({ scope: "ids", ids: [3, 4] });
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/requests?scope=ids&ids=3%2C4");
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: "DELETE", credentials: "same-origin" });
  });

  it("returns undefined for 204 responses", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 204 })),
    );
    await expect(api.deleteRequest(5)).resolves.toBeUndefined();
  });
});
