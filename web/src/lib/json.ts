/*
  A small JSON parser for display. Unlike JSON.parse it keeps number
  literals exactly as sent (no rounding of large integers) and keeps
  duplicate keys in order, because the point of an inspector is to show
  what arrived.
*/

export type JsonNode =
  | { type: "object"; entries: [string, JsonNode][] }
  | { type: "array"; items: JsonNode[] }
  | { type: "string"; value: string }
  | { type: "number"; raw: string }
  | { type: "boolean"; value: boolean }
  | { type: "null" };

export class JsonParseError extends Error {
  readonly position: number;
  constructor(message: string, position: number) {
    super(message);
    this.name = "JsonParseError";
    this.position = position;
  }
}

export class JsonTooLargeError extends Error {
  constructor() {
    super("The document is too large to show as a tree.");
    this.name = "JsonTooLargeError";
  }
}

const MAX_DEPTH = 256;
const numberPattern = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;

export function parseJson(text: string, maxNodes = 50_000): JsonNode {
  let pos = text.charCodeAt(0) === 0xfeff ? 1 : 0;
  let nodes = 0;

  const fail = (msg: string): never => {
    const before = text.slice(0, pos);
    const line = before.split("\n").length;
    const column = pos - before.lastIndexOf("\n");
    throw new JsonParseError(`${msg} at line ${line}, column ${column}`, pos);
  };
  const skip = () => {
    while (pos < text.length) {
      const c = text.charCodeAt(pos);
      if (c === 32 || c === 9 || c === 10 || c === 13) pos++;
      else break;
    }
  };
  const count = () => {
    if (++nodes > maxNodes) throw new JsonTooLargeError();
  };

  const parseString = (): string => {
    const start = pos;
    pos++; // opening quote
    while (pos < text.length) {
      const c = text.charCodeAt(pos);
      if (c === 34) {
        pos++;
        try {
          return JSON.parse(text.slice(start, pos)) as string;
        } catch {
          pos = start;
          return fail("Invalid string");
        }
      }
      if (c === 92) pos += 2;
      else if (c < 32) return fail("Control character in string");
      else pos++;
    }
    return fail("Unterminated string");
  };

  const parseValue = (depth: number): JsonNode => {
    if (depth > MAX_DEPTH) fail("Nesting is too deep");
    skip();
    count();
    const c = text[pos];
    switch (c) {
      case "{": {
        pos++;
        const entries: [string, JsonNode][] = [];
        skip();
        if (text[pos] === "}") {
          pos++;
          return { type: "object", entries };
        }
        for (;;) {
          skip();
          if (text[pos] !== '"') fail("Expected a property name");
          const key = parseString();
          skip();
          if (text[pos] !== ":") fail("Expected ':'");
          pos++;
          entries.push([key, parseValue(depth + 1)]);
          skip();
          if (text[pos] === ",") {
            pos++;
            continue;
          }
          if (text[pos] === "}") {
            pos++;
            return { type: "object", entries };
          }
          fail("Expected ',' or '}'");
        }
      }
      case "[": {
        pos++;
        const items: JsonNode[] = [];
        skip();
        if (text[pos] === "]") {
          pos++;
          return { type: "array", items };
        }
        for (;;) {
          items.push(parseValue(depth + 1));
          skip();
          if (text[pos] === ",") {
            pos++;
            continue;
          }
          if (text[pos] === "]") {
            pos++;
            return { type: "array", items };
          }
          fail("Expected ',' or ']'");
        }
      }
      case '"':
        return { type: "string", value: parseString() };
      case "t":
        if (text.startsWith("true", pos)) {
          pos += 4;
          return { type: "boolean", value: true };
        }
        break;
      case "f":
        if (text.startsWith("false", pos)) {
          pos += 5;
          return { type: "boolean", value: false };
        }
        break;
      case "n":
        if (text.startsWith("null", pos)) {
          pos += 4;
          return { type: "null" };
        }
        break;
      default: {
        numberPattern.lastIndex = pos;
        const m = numberPattern.exec(text);
        if (m) {
          pos += m[0].length;
          return { type: "number", raw: m[0] };
        }
      }
    }
    return pos >= text.length ? fail("Unexpected end of input") : fail(`Unexpected character '${text[pos]}'`);
  };

  const root = parseValue(0);
  skip();
  if (pos < text.length) fail("Unexpected data after the JSON value");
  return root;
}

export function formatJson(node: JsonNode, indent = 2): string {
  const out: string[] = [];
  const pad = (depth: number) => " ".repeat(depth * indent);
  const walk = (n: JsonNode, depth: number) => {
    switch (n.type) {
      case "object":
        if (n.entries.length === 0) {
          out.push("{}");
          return;
        }
        out.push("{\n");
        n.entries.forEach(([k, v], i) => {
          out.push(pad(depth + 1), JSON.stringify(k), ": ");
          walk(v, depth + 1);
          out.push(i < n.entries.length - 1 ? ",\n" : "\n");
        });
        out.push(pad(depth), "}");
        return;
      case "array":
        if (n.items.length === 0) {
          out.push("[]");
          return;
        }
        out.push("[\n");
        n.items.forEach((v, i) => {
          out.push(pad(depth + 1));
          walk(v, depth + 1);
          out.push(i < n.items.length - 1 ? ",\n" : "\n");
        });
        out.push(pad(depth), "]");
        return;
      case "string":
        out.push(JSON.stringify(n.value));
        return;
      case "number":
        out.push(n.raw);
        return;
      case "boolean":
        out.push(String(n.value));
        return;
      case "null":
        out.push("null");
    }
  };
  walk(node, 0);
  return out.join("");
}

/** Pretty-prints JSON text, or returns null when it does not parse. */
export function prettyJson(text: string): string | null {
  try {
    return formatJson(parseJson(text, 200_000));
  } catch {
    return null;
  }
}

export type TokenType = "key" | "string" | "number" | "keyword" | "punct" | "plain";
export interface Token {
  type: TokenType;
  text: string;
}

/**
 * Splits JSON-like text into tokens for highlighting. It never fails:
 * anything it does not recognize becomes plain text.
 */
export function tokenizeJson(text: string): Token[] {
  const tokens: Token[] = [];
  let pos = 0;
  const push = (type: TokenType, t: string) => {
    const last = tokens[tokens.length - 1];
    if (last && last.type === type && (type === "plain" || type === "punct")) last.text += t;
    else tokens.push({ type, text: t });
  };
  while (pos < text.length) {
    const c = text[pos];
    if (c === '"') {
      let end = pos + 1;
      while (end < text.length && text[end] !== '"' && text[end] !== "\n") end += text[end] === "\\" ? 2 : 1;
      end = Math.min(end + 1, text.length);
      const str = text.slice(pos, end);
      let look = end;
      while (look < text.length && (text[look] === " " || text[look] === "\t")) look++;
      push(text[look] === ":" ? "key" : "string", str);
      pos = end;
      continue;
    }
    if (c === "{" || c === "}" || c === "[" || c === "]" || c === ":" || c === ",") {
      push("punct", c);
      pos++;
      continue;
    }
    numberPattern.lastIndex = pos;
    const m = c === "-" || (c >= "0" && c <= "9") ? numberPattern.exec(text) : null;
    if (m) {
      push("number", m[0]);
      pos += m[0].length;
      continue;
    }
    const word = /^(true|false|null)/.exec(text.slice(pos, pos + 5));
    if (word) {
      push("keyword", word[1]);
      pos += word[1].length;
      continue;
    }
    push("plain", c);
    pos++;
  }
  return tokens;
}

export function countNodes(node: JsonNode): number {
  switch (node.type) {
    case "object":
      return 1 + node.entries.reduce((n, [, v]) => n + countNodes(v), 0);
    case "array":
      return 1 + node.items.reduce((n, v) => n + countNodes(v), 0);
    default:
      return 1;
  }
}
