import { useState, useRef, useEffect } from "react";
import type { ChannelInfo } from "@/types/instance";

// Channel display metadata
const CHANNEL_META: Record<
    string,
    { label: string; color: string; hoverColor: string }
> = {
    slack: {
        label: "Slack",
        color: "#4A154B",
        hoverColor: "#611f64",
    },
    discord: {
        label: "Discord",
        color: "#5865F2",
        hoverColor: "#4752c4",
    },
    whatsapp: {
        label: "WhatsApp",
        color: "#25D366",
        hoverColor: "#1da851",
    },
    telegram: {
        label: "Telegram",
        color: "#26A5E4",
        hoverColor: "#1e8cbf",
    },
    signal: {
        label: "Signal",
        color: "#3A76F0",
        hoverColor: "#2e5ec0",
    },
    imessage: {
        label: "iMessage",
        color: "#34C759",
        hoverColor: "#2aa147",
    },
    msteams: {
        label: "MS Teams",
        color: "#6264A7",
        hoverColor: "#4e5086",
    },
    googlechat: {
        label: "Google Chat",
        color: "#00AC47",
        hoverColor: "#008a39",
    },
};

// Inline SVG logos for each channel
function ChannelLogo({
    type,
    size = 20,
}: {
    type: string;
    size?: number;
}) {
    const s = size;
    switch (type) {
        case "slack":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52v2.52zm1.271 0a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522v-6.313zM8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52H8.834zm0 1.271a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521h6.312zm6.29 2.521a2.528 2.528 0 0 1 2.52-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.356 2.521h-2.52V8.834zm-1.271 0a2.527 2.527 0 0 1-2.521 2.521 2.528 2.528 0 0 1-2.521-2.521V2.522A2.528 2.528 0 0 1 11.332 0a2.528 2.528 0 0 1 2.521 2.522v6.312zm-2.521 6.29a2.528 2.528 0 0 1 2.521 2.52A2.528 2.528 0 0 1 15.166 24a2.527 2.527 0 0 1-2.521-2.356v-2.52h2.521zm0-1.271a2.527 2.527 0 0 1-2.521-2.521 2.528 2.528 0 0 1 2.521-2.521h6.312A2.528 2.528 0 0 1 24 15.166a2.528 2.528 0 0 1-2.522 2.521h-6.312z" />
                </svg>
            );
        case "discord":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028c.462-.63.874-1.295 1.226-1.994a.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.095 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.095 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z" />
                </svg>
            );
        case "whatsapp":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 0 1-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 0 1-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 0 1 2.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0 0 12.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 0 0 5.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 0 0-3.48-8.413z" />
                </svg>
            );
        case "telegram":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.479.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z" />
                </svg>
            );
        case "signal":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M12 0C5.373 0 0 5.373 0 12s5.373 12 12 12 12-5.373 12-12S18.627 0 12 0zm5.894 17.08l-1.058-.609c-.27-.156-.606-.156-.876 0l-.308.178a5.457 5.457 0 01-5.455-.001l-.308-.177a.877.877 0 00-.876 0l-1.057.61a.25.25 0 01-.346-.092l-.495-.857a.25.25 0 01.092-.341l1.058-.61a.877.877 0 00.438-.758v-.356a5.457 5.457 0 012.728-4.726l.308-.178c.27-.156.438-.444.438-.759v-.356a.877.877 0 00-.438-.759L10.68 7.284a.25.25 0 01-.092-.341l.495-.857a.25.25 0 01.346-.092l1.057.61c.27.156.606.156.876 0l.308-.178A5.457 5.457 0 0116.398 6l.308.178c.27.156.438.444.438.758v.356c0 .314.168.604.438.759l1.058.609a.25.25 0 01.092.341l-.495.857a.25.25 0 01-.346.092l-1.058-.61a.877.877 0 00-.876 0l-.308.178a5.457 5.457 0 01-2.728 4.726l-.308.178a.877.877 0 00-.438.758v.357c0 .314.168.602.438.758l1.058.61a.25.25 0 01.092.341l-.495.857a.25.25 0 01-.346.092z" />
                </svg>
            );
        case "imessage":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M5.285 0A5.273 5.273 0 0 0 0 5.285v13.43A5.273 5.273 0 0 0 5.285 24h13.43A5.273 5.273 0 0 0 24 18.715V5.285A5.273 5.273 0 0 0 18.715 0H5.285zm6.802 4.752c4.152 0 6.9 2.673 6.9 5.937 0 3.264-2.868 5.937-6.9 5.937-.636 0-1.308-.072-1.944-.252-.468.396-1.524 1.116-3.024 1.548-.108.036-.216-.072-.18-.18.252-.504.516-1.404.516-2.052-.012-.036-.012-.036-.024-.072-1.344-1.056-2.244-2.628-2.244-4.393 0-3.816 2.748-6.473 6.9-6.473z" />
                </svg>
            );
        case "msteams":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M20.625 8.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4zm-.008 1H18.25a.25.25 0 0 0-.25.25v5.875A3.375 3.375 0 0 1 14.625 19h-3.613a4.239 4.239 0 0 0 8.613-.77V9.75a.25.25 0 0 0-.008-.25zM16.5 6a3 3 0 1 0-6 0 3 3 0 0 0 6 0zm-2 3h-4.75a1.25 1.25 0 0 0-1.25 1.25v5.5A4.25 4.25 0 0 0 12.75 20a4.25 4.25 0 0 0 4.25-4.25v-5.5A1.25 1.25 0 0 0 15.75 9H14.5zM5.75 10a1.75 1.75 0 1 0 0-3.5 1.75 1.75 0 0 0 0 3.5zm0 1c-.897 0-1.633.36-2.164.97C2.975 12.673 2.5 13.753 2.5 15v1a.5.5 0 0 0 .5.5h3.375a5.226 5.226 0 0 1-.375-1.75v-5.5c0-.098.012-.173.012-.25H5.75z" />
                </svg>
            );
        case "googlechat":
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M12 0C5.372 0 0 5.373 0 12s5.372 12 12 12c6.627 0 12-5.373 12-12S18.627 0 12 0zm5.568 16.4h-3.535L12 13.867 9.967 16.4H6.432l3.84-4.8L6.432 6.8h3.535L12 9.333 14.033 6.8h3.535l-3.84 4.8 3.84 4.8z" />
                </svg>
            );
        default:
            // Generic chat icon
            return (
                <svg viewBox="0 0 24 24" width={s} height={s} fill="currentColor">
                    <path d="M12 2C6.477 2 2 6.477 2 12c0 1.82.487 3.53 1.338 5.002L2.08 21.37a.75.75 0 0 0 .92.92l4.368-1.258A9.953 9.953 0 0 0 12 22c5.523 0 10-4.477 10-10S17.523 2 12 2z" />
                </svg>
            );
    }
}

// Tooltip component
function Tooltip({
    children,
    content,
}: {
    children: React.ReactNode;
    content: React.ReactNode;
}) {
    const [visible, setVisible] = useState(false);
    const [position, setPosition] = useState<"top" | "bottom">("top");
    const triggerRef = useRef<HTMLDivElement>(null);
    const tooltipRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (visible && triggerRef.current) {
            const rect = triggerRef.current.getBoundingClientRect();
            // If too close to the top, show below
            if (rect.top < 100) {
                setPosition("bottom");
            } else {
                setPosition("top");
            }
        }
    }, [visible]);

    return (
        <div
            ref={triggerRef}
            className="relative inline-flex"
            onMouseEnter={() => setVisible(true)}
            onMouseLeave={() => setVisible(false)}
        >
            {children}
            {visible && (
                <div
                    ref={tooltipRef}
                    className={`absolute z-50 px-3 py-2 text-xs font-medium text-white bg-gray-900 rounded-lg shadow-lg backdrop-blur-sm whitespace-nowrap pointer-events-none
            ${position === "top" ? "bottom-full mb-2" : "top-full mt-2"}
            left-1/2 -translate-x-1/2`}
                >
                    {content}
                    <div
                        className={`absolute left-1/2 -translate-x-1/2 w-2 h-2 bg-gray-900 rotate-45
              ${position === "top" ? "-bottom-1" : "-top-1"}`}
                    />
                </div>
            )}
        </div>
    );
}

export default function ChannelIcons({
    channels,
}: {
    channels: ChannelInfo[];
}) {
    if (!channels || channels.length === 0) {
        return (
            <span className="text-xs text-gray-400 italic">—</span>
        );
    }

    return (
        <div className="flex items-center gap-1.5 flex-wrap">
            {channels.map((channel) => {
                const meta = CHANNEL_META[channel.type] || {
                    label: channel.type,
                    color: "#6B7280",
                    hoverColor: "#4B5563",
                };

                // Build tooltip content
                const tooltipContent = (
                    <div className="space-y-1">
                        <div className="font-semibold text-sm">{meta.label}</div>
                        {channel.accounts.map((acc, i) => {
                            const isDisabled = acc.enabled === false;
                            return (
                                <div key={i} className="space-y-0.5">
                                    {channel.accounts.length > 1 && (
                                        <div className={`text-xs ${isDisabled ? "text-gray-400 line-through" : "text-gray-300"}`}>
                                            {acc.name}
                                            {isDisabled && " (disabled)"}
                                        </div>
                                    )}
                                    {acc.groups && acc.groups.length > 0 && (
                                        <div className="text-xs text-gray-400 pl-2">
                                            {acc.groups.map((g, gi) => (
                                                <div key={gi}>• {g}</div>
                                            ))}
                                        </div>
                                    )}
                                </div>
                            );
                        })}
                    </div>
                );

                // Check if all accounts are disabled
                const allDisabled = channel.accounts.every(
                    (a) => a.enabled === false,
                );

                return (
                    <Tooltip key={channel.type} content={tooltipContent}>
                        <div
                            className={`flex items-center justify-center w-7 h-7 rounded-md transition-all duration-150 cursor-default
                ${allDisabled ? "opacity-30 grayscale" : "hover:scale-110 hover:shadow-md"}`}
                            style={{ color: allDisabled ? "#9CA3AF" : meta.color }}
                        >
                            <ChannelLogo type={channel.type} size={18} />
                        </div>
                    </Tooltip>
                );
            })}
        </div>
    );
}
