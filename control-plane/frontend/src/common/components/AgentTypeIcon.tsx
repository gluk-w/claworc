import { useState } from "react";
import { Bot } from "lucide-react";

// Agent types with a logo published at claworc.com. Custom/unknown types
// never hit the network — they always get the generic Bot glyph.
const HOSTED_ICON_TYPES = new Set(["openclaw", "hermes", "nanoclaw"]);

interface AgentTypeIconProps {
  agentType: string;
  title?: string;
  className?: string;
}

export default function AgentTypeIcon({ agentType, title, className = "w-5 h-5" }: AgentTypeIconProps) {
  const [failed, setFailed] = useState(false);

  if (failed || !HOSTED_ICON_TYPES.has(agentType)) {
    return (
      <span title={title} className="inline-flex shrink-0">
        <Bot className={`${className} text-gray-400`} aria-label={title ?? agentType} />
      </span>
    );
  }

  return (
    <img
      src={`https://claworc.com/public/${agentType}.svg`}
      alt={title ?? agentType}
      title={title}
      // Cloudflare hotlink protection allows refererless requests, and this
      // keeps dashboard URLs out of claworc.com logs.
      referrerPolicy="no-referrer"
      className={`${className} shrink-0`}
      onError={() => setFailed(true)}
    />
  );
}
