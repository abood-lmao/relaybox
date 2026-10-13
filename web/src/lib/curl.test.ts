import { describe, expect, it } from "vitest";
import { body, detail } from "../test/fixtures";
import { endpointExamples, requestToCurl, shellQuote } from "./curl";

describe("shellQuote", () => {
  it("leaves safe values alone and quotes the rest", () => {
    expect(shellQuote("https://x.test/a?b=1")).toBe("'https://x.test/a?b=1'");
    expect(shellQuote("simple-value_1.0")).toBe("simple-value_1.0");
    expect(shellQuote("it's")).toBe(`'it'"'"'s'`);
    expect(shellQuote("$(rm -rf /)")).toBe("'$(rm -rf /)'");
  });
});

describe("requestToCurl", () => {
  it("rebuilds the request without framing or redacted headers", () => {
    const req = detail({
      method: "PUT",
      headers: [
        ["Authorization", "Bearer [REDACTED]"],
        ["Content-Length", "16"],
        ["Content-Type", "application/json"],
        ["Host", "127.0.0.1:8787"],
        ["X-Custom", "a'b"],
      ],
    });
    const out = requestToCurl(req);
    expect(out.command).toContain("curl -X PUT http://127.0.0.1:8787/hooks/pub1/events");
    expect(out.command).toContain("-H 'Content-Type: application/json'");
    expect(out.command).toContain(`-H 'X-Custom: a'"'"'b'`);
    expect(out.command).toContain(`--data-binary '{"event":"test"}'`);
    expect(out.command).not.toContain("Authorization");
    expect(out.command).not.toContain("Content-Length");
    expect(out.command).not.toContain("Host:");
    expect(out.omittedHeaders).toEqual(["Authorization"]);
    expect(out.bodyOmitted).toBe(false);
  });

  it("leaves out binary and truncated bodies", () => {
    expect(
      requestToCurl(detail({ body: body({ encoding: "base64", data: "iVBORw0K", kind: "binary" }) }))
        .bodyOmitted,
    ).toBe(true);
    expect(requestToCurl(detail({ body: body({ truncated: true }) })).bodyOmitted).toBe(true);
    expect(requestToCurl(detail({ body: body({ kind: "empty", data: "", size: 0 }) })).command).not.toContain(
      "--data",
    );
  });
});

describe("endpointExamples", () => {
  it("builds runnable examples for the endpoint URL", () => {
    const examples = endpointExamples("http://127.0.0.1:8787/hooks/abc");
    expect(examples.map((e) => e.label)).toEqual(["JSON", "Form", "GET with query"]);
    expect(examples[0].command).toContain("curl -X POST http://127.0.0.1:8787/hooks/abc");
    expect(examples[2].command).toBe("curl 'http://127.0.0.1:8787/hooks/abc?source=docs&attempt=1'");
  });
});
