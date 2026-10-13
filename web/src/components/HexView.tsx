import { useMemo } from "react";

export const HEX_PREVIEW_BYTES = 4096;

function hexLines(bytes: Uint8Array): string[] {
  const lines: string[] = [];
  for (let off = 0; off < bytes.length; off += 16) {
    const chunk = bytes.subarray(off, off + 16);
    let hex = "";
    let ascii = "";
    for (let i = 0; i < 16; i++) {
      if (i < chunk.length) {
        hex += chunk[i].toString(16).padStart(2, "0") + " ";
        ascii += chunk[i] >= 0x20 && chunk[i] < 0x7f ? String.fromCharCode(chunk[i]) : ".";
      } else {
        hex += "   ";
      }
      if (i === 7) hex += " ";
    }
    lines.push(`${off.toString(16).padStart(8, "0")}  ${hex} ${ascii}`);
  }
  return lines;
}

/** Hex dump of the first bytes of a binary body. Nothing is decoded or run. */
export function HexView({ bytes }: { bytes: Uint8Array }) {
  const text = useMemo(() => hexLines(bytes.subarray(0, HEX_PREVIEW_BYTES)).join("\n"), [bytes]);
  return (
    <pre
      tabIndex={0}
      aria-label="Hex dump"
      className="scroll-thin overflow-x-auto font-mono text-[12px] leading-[1.6] whitespace-pre text-fg"
    >
      {text}
    </pre>
  );
}
