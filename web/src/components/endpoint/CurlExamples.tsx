import { useState } from "react";
import { endpointExamples } from "../../lib/curl";
import { CodeView } from "../CodeView";
import { CopyButton } from "../ui/misc";
import { Tabs } from "../ui/tabs";

export function CurlExamples({ url }: { url: string }) {
  const examples = endpointExamples(url);
  const [active, setActive] = useState(examples[0].label);
  const current = examples.find((e) => e.label === active) ?? examples[0];
  return (
    <div className="rounded-md border border-line">
      <div className="flex items-center justify-between gap-2 border-b border-line px-2 py-1.5">
        <Tabs
          size="sm"
          label="Example request"
          tabs={examples.map((e) => ({ key: e.label, label: e.label }))}
          value={active}
          onChange={setActive}
        />
        <CopyButton text={current.command} label="Copy command" showLabel />
      </div>
      <div className="bg-surface-2/50 px-3 py-2.5">
        <CodeView text={current.command} label="curl command" />
      </div>
    </div>
  );
}
