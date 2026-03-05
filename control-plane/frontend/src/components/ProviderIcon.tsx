/**
 * Thin wrapper around @lobehub/icons provider brand icons.
 * Imports only Mono/Color sub-path components to avoid pulling in
 * @lobehub/ui / antd (which the features/ module requires).
 */
import type { FC, CSSProperties } from "react";

// Mono (black/white SVG) components
import AnthropicMono from "@lobehub/icons/es/Anthropic/components/Mono";
import MoonshotMono from "@lobehub/icons/es/Moonshot/components/Mono";
import OpenRouterMono from "@lobehub/icons/es/OpenRouter/components/Mono";
import OllamaMono from "@lobehub/icons/es/Ollama/components/Mono";

// Color SVG components
import MinimaxColor from "@lobehub/icons/es/Minimax/components/Color";
import KimiColor from "@lobehub/icons/es/Kimi/components/Color";
import TogetherColor from "@lobehub/icons/es/Together/components/Color";
import VolcengineColor from "@lobehub/icons/es/Volcengine/components/Color";
import DoubaoColor from "@lobehub/icons/es/Doubao/components/Color";
import HuggingFaceColor from "@lobehub/icons/es/HuggingFace/components/Color";
import NvidiaColor from "@lobehub/icons/es/Nvidia/components/Color";
import BaiduColor from "@lobehub/icons/es/Baidu/components/Color";
import QwenColor from "@lobehub/icons/es/Qwen/components/Color";
import BedrockColor from "@lobehub/icons/es/Bedrock/components/Color";

type IconComponent = FC<{ size?: number; style?: CSSProperties; className?: string }>;

const ICON_MAP: Record<string, IconComponent> = {
  anthropic: AnthropicMono,
  minimax: MinimaxColor,
  moonshot: MoonshotMono,
  togetherai: TogetherColor,
  volcengine: VolcengineColor,
  doubao: DoubaoColor,
  huggingface: HuggingFaceColor,
  nvidia: NvidiaColor,
  openrouter: OpenRouterMono,
  baidu: BaiduColor,
  qwen: QwenColor,
  ollama: OllamaMono,
  bedrock: BedrockColor,
  kimi: KimiColor,
};

interface ProviderIconProps {
  provider: string;
  size?: number;
  className?: string;
  style?: CSSProperties;
}

export default function ProviderIcon({ provider, size = 20, className, style }: ProviderIconProps) {
  const Icon = ICON_MAP[provider];
  if (!Icon) return null;
  return <Icon size={size} className={className} style={style} />;
}
