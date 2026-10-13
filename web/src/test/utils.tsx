import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { vi } from "vitest";
import { App } from "../App";
import { createQueryClient } from "../lib/queryClient";
import { meta, stats } from "./fixtures";

export interface MockRequest {
  method: string;
  path: string;
  url: URL;
  body: unknown;
}

export interface MockResponse {
  status?: number;
  body?: unknown;
  headers?: Record<string, string>;
}

type Route = ((req: MockRequest) => MockResponse | Promise<MockResponse>) | MockResponse;

/**
 * Replaces fetch with a router keyed by "METHOD /path". Unmatched calls get
 * a 404 API error. Every call is recorded.
 */
export function mockApi(routes: Record<string, Route>) {
  const calls: MockRequest[] = [];
  const all: Record<string, Route> = {
    "GET /api/v1/auth/session": { body: { auth_required: false, authenticated: true } },
    "GET /api/v1/meta": { body: meta },
    "GET /api/v1/stats": { body: stats() },
    "GET /api/v1/endpoints": { body: { items: [] } },
    "GET /api/v1/requests": { body: { items: [], total: 0, limit: 50, offset: 0 } },
    ...routes,
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input.toString(), "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    let body: unknown;
    if (typeof init?.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    const req: MockRequest = { method, path: url.pathname, url, body };
    calls.push(req);
    const route = all[`${method} ${url.pathname}`];
    if (!route) {
      return json(404, { error: { code: "not_found", message: `No mock for ${method} ${url.pathname}` } });
    }
    const res = typeof route === "function" ? await route(req) : route;
    return json(res.status ?? 200, res.body, res.headers);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock };
}

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  const noBody = status === 204 || body === undefined;
  return new Response(noBody ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

export function renderApp(path = "/") {
  const client = createQueryClient();
  client.setDefaultOptions({ queries: { retry: false, staleTime: Infinity, refetchOnWindowFocus: false } });
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App queryClient={client} />
    </MemoryRouter>,
  );
}
