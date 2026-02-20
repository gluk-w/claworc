import {
  Brain,
  Sparkles,
  Globe,
  Wind,
  Cpu,
  Search,
  Users,
  Flame,
  CircuitBoard,
  Atom,
  Layers,
  MessageCircleQuestion,
  Route,
  Shield,
  Crown,
  Code,
  Target,
  Network,
  Wrench,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ProviderCategory } from "./providerData";

/** Maps provider IDs to lucide-react icons */
export const PROVIDER_ICONS: Record<string, LucideIcon> = {
  anthropic: Brain,
  openai: Sparkles,
  google: Globe,
  mistral: Wind,
  groq: Cpu,
  deepseek: Search,
  together: Users,
  fireworks: Flame,
  cerebras: CircuitBoard,
  xai: Atom,
  cohere: Layers,
  perplexity: MessageCircleQuestion,
  openrouter: Route,
  brave: Shield,
};

/** Maps category names to lucide-react icons */
export const CATEGORY_ICONS: Record<ProviderCategory, LucideIcon> = {
  "Major Providers": Crown,
  "Open Source / Inference": Code,
  Specialized: Target,
  Aggregators: Network,
  "Search & Tools": Wrench,
};
