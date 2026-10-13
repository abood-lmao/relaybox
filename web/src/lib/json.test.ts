import { describe, expect, it } from "vitest";
import {
  countNodes,
  formatJson,
  JsonParseError,
  JsonTooLargeError,
  parseJson,
  prettyJson,
  tokenizeJson,
} from "./json";

describe("parseJson", () => {
  it("keeps number literals exactly as sent", () => {
    const node = parseJson('{"id": 9007199254740993, "price": 1.10, "exp": 1e400}');
    expect(formatJson(node, 0).replace(/\n/g, "")).toContain("9007199254740993");
    expect(formatJson(node)).toContain('"price": 1.10');
    expect(formatJson(node)).toContain('"exp": 1e400');
  });

  it("keeps duplicate keys in order", () => {
    const node = parseJson('{"a": 1, "a": 2}');
    expect(node).toEqual({
      type: "object",
      entries: [
        ["a", { type: "number", raw: "1" }],
        ["a", { type: "number", raw: "2" }],
      ],
    });
  });

  it("decodes string escapes", () => {
    const node = parseJson('"line\\nbreak \\u00e9 \\ud83d\\ude00"');
    expect(node).toEqual({ type: "string", value: "line\nbreak é 😀" });
  });

  it("accepts a byte order mark and surrounding whitespace", () => {
    expect(parseJson("\uFEFF  [true, false, null]  ").type).toBe("array");
  });

  it.each([
    ["[", "Unexpected end of input"],
    ["{", "Expected a property name"],
    ['{"a" 1}', "Expected ':'"],
    ["[1,]", "Unexpected character"],
    ["[1] x", "Unexpected data after"],
    ['"tab\there"', "Control character"],
    ["01", "Unexpected data after"],
  ])("rejects %s", (text, message) => {
    expect(() => parseJson(text)).toThrow(JsonParseError);
    expect(() => parseJson(text)).toThrow(message);
  });

  it("reports line and column", () => {
    try {
      parseJson('{\n  "a": tru\n}');
      expect.unreachable();
    } catch (e) {
      expect((e as Error).message).toMatch(/line 2, column 8/);
    }
  });

  it("limits nesting depth", () => {
    expect(() => parseJson("[".repeat(300) + "]".repeat(300))).toThrow("Nesting is too deep");
  });

  it("limits the number of values", () => {
    expect(() => parseJson(JSON.stringify(Array.from({ length: 100 }, (_, i) => i)), 50)).toThrow(
      JsonTooLargeError,
    );
  });
});

describe("formatJson and helpers", () => {
  it("pretty prints with two-space indentation", () => {
    expect(formatJson(parseJson('{"a":[1,{"b":null}],"c":{}}'))).toBe(
      '{\n  "a": [\n    1,\n    {\n      "b": null\n    }\n  ],\n  "c": {}\n}',
    );
  });

  it("returns null for invalid JSON", () => {
    expect(prettyJson("{nope")).toBeNull();
    expect(prettyJson("[]")).toBe("[]");
  });

  it("counts nodes", () => {
    expect(countNodes(parseJson('{"a":[1,2],"b":"x"}'))).toBe(5);
  });
});

describe("tokenizeJson", () => {
  it("labels keys, strings, numbers, and keywords", () => {
    const tokens = tokenizeJson('{"k": "v", "n": -1.5e3, "t": true}');
    const types = tokens.filter((t) => t.type !== "punct" && t.type !== "plain").map((t) => [t.type, t.text]);
    expect(types).toEqual([
      ["key", '"k"'],
      ["string", '"v"'],
      ["key", '"n"'],
      ["number", "-1.5e3"],
      ["key", '"t"'],
      ["keyword", "true"],
    ]);
  });

  it("never fails on invalid input and preserves the text", () => {
    const text = '{"open": "unterminated\n<script>alert(1)</script>';
    expect(
      tokenizeJson(text)
        .map((t) => t.text)
        .join(""),
    ).toBe(text);
  });
});
