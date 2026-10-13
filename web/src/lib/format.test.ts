import { describe, expect, it } from "vitest";
import {
  base64ToBytes,
  formatBytes,
  formatDuration,
  formatRelative,
  pairsToText,
  plural,
  textToBase64,
} from "./format";
import { displayQuery, isRedacted, statusClass } from "./http";

describe("format", () => {
  it.each([
    [0, "0 B"],
    [1023, "1023 B"],
    [1024, "1 KiB"],
    [1536, "1.5 KiB"],
    [1048576, "1 MiB"],
    [150 * 1048576, "150 MiB"],
  ])("formatBytes(%i) = %s", (n, want) => {
    expect(formatBytes(n)).toBe(want);
  });

  it.each([
    [0.4, "<1 ms"],
    [12, "12 ms"],
    [1500, "1.50 s"],
    [12_000, "12.0 s"],
    [125_000, "2 min 5 s"],
  ])("formatDuration(%d) = %s", (ms, want) => {
    expect(formatDuration(ms)).toBe(want);
  });

  it("formats relative times", () => {
    const now = Date.parse("2026-10-10T12:00:00Z");
    expect(formatRelative("2026-10-10T11:59:58Z", now)).toBe("just now");
    expect(formatRelative("2026-10-10T11:58:00Z", now)).toBe("2 min ago");
    expect(formatRelative("2026-10-10T09:00:00Z", now)).toBe("3 h ago");
    expect(formatRelative("2026-10-07T12:00:00Z", now)).toBe("3 d ago");
  });

  it("pluralizes", () => {
    expect(plural(1, "request")).toBe("1 request");
    expect(plural(2, "request")).toBe("2 requests");
    expect(plural(1200, "entry", "entries")).toBe("1,200 entries");
  });

  it("round-trips base64 with UTF-8", () => {
    const encoded = textToBase64("héllo ✓");
    expect(new TextDecoder().decode(base64ToBytes(encoded))).toBe("héllo ✓");
  });

  it("formats pairs as header lines", () => {
    expect(
      pairsToText([
        ["A", "1"],
        ["B", "2"],
      ]),
    ).toBe("A: 1\nB: 2");
  });
});

describe("http helpers", () => {
  it("classifies statuses", () => {
    expect([101, 204, 302, 404, 503].map(statusClass)).toEqual(["1xx", "2xx", "3xx", "4xx", "5xx"]);
  });

  it("decodes encoded redaction placeholders in query strings", () => {
    expect(displayQuery("a=1&token=%5BREDACTED%5D&b=%5bredacted%5d")).toBe(
      "a=1&token=[REDACTED]&b=[REDACTED]",
    );
    expect(isRedacted("Bearer [REDACTED]")).toBe(true);
    expect(isRedacted("Bearer abc")).toBe(false);
  });
});
