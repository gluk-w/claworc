import { getLobeIconCDN } from "@lobehub/icons/es/features/getLobeIconCDN";
import type { CSSProperties } from "react";

interface ProviderIconProps {
  provider: string;
  size?: number;
  className?: string;
  style?: CSSProperties;
}

export default function ProviderIcon({ provider, size = 20, className, style }: ProviderIconProps) {
  const url = getLobeIconCDN(provider, { type: "mono", format: "png", cdn: "unpkg" });
  return (
    <img
      src={url}
      alt={provider}
      width={size}
      height={size}
      className={className}
      style={{ display: "inline-block", ...style }}
      onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = "none"; }}
    />
  );
}
