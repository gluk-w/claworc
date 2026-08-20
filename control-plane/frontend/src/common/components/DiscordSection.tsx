import { useEffect, useState } from "react";
import { useInstanceDiscord, useUpdateInstanceDiscord } from "@common/hooks/useDiscord";
import type { DiscordChannelRule, InstanceDiscordUpdatePayload } from "@common/types/discord";
import { successToast, errorToast, infoToast } from "@common/utils/toast";
import DiscordChannelsEditor, { DiscordDMPolicySelect } from "@common/components/DiscordChannelsEditor";

interface Props {
  instanceId: number;
}

/**
 * Per-agent Discord connection card (settings tab). The bot token is stored
 * as an encrypted env var (DISCORD_BOT_TOKEN) — changing it restarts the
 * agent container; server/channel/DM changes apply live.
 */
export default function DiscordSection({ instanceId }: Props) {
  const { data, isLoading, error } = useInstanceDiscord(instanceId);
  const updateMut = useUpdateInstanceDiscord(instanceId);

  const [enabled, setEnabled] = useState(false);
  const [channels, setChannels] = useState<DiscordChannelRule[]>([]);
  const [dmPolicy, setDmPolicy] = useState("");
  const [botToken, setBotToken] = useState("");
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (!data || dirty) return;
    setEnabled(data.enabled);
    setChannels(data.channels);
    setDmPolicy(data.dm_policy ?? "");
  }, [data, dirty]);

  const markDirty = () => setDirty(true);

  async function handleSave() {
    const payload: InstanceDiscordUpdatePayload = {
      enabled,
      channels: channels.filter((c) => c.guild_id.trim() !== ""),
      dm_policy: dmPolicy,
    };
    // The token is only sent when the user typed one — an empty field means
    // "keep the current token" (remove via the Environment Variables card).
    if (botToken.trim()) payload.bot_token = botToken.trim();

    try {
      const res = await updateMut.mutateAsync(payload);
      setBotToken("");
      setDirty(false);
      if (res.restarting) {
        infoToast("Discord settings saved", "Restarting the agent to apply the new token…");
      } else {
        successToast("Discord settings saved");
      }
    } catch (e: any) {
      errorToast("Failed to save Discord settings", e);
    }
  }

  if (isLoading) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900">Discord</h3>
        <p className="text-xs text-gray-500 mt-2">Loading…</p>
      </div>
    );
  }
  if (error) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900">Discord</h3>
        <p className="text-xs text-red-600 mt-2">Failed to load Discord config.</p>
      </div>
    );
  }

  const missingToken = enabled && !data?.has_bot_token && !botToken.trim();

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <h3 className="text-sm font-medium text-gray-900 mb-1">Discord</h3>
      <p className="text-xs text-gray-500 mb-4">
        Connect this agent to Discord with a bot token. The token is stored encrypted and never
        written into the agent's config file. The bot's Message Content Intent must be enabled in
        the Discord Developer Portal.
      </p>

      <div className="space-y-4">
        <label className="flex items-start gap-2 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => {
              setEnabled(e.target.checked);
              markDirty();
            }}
            className="mt-0.5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
          <span>
            <span className="block text-sm text-gray-900">Enable Discord</span>
            <span className="block text-xs text-gray-500">
              The agent joins Discord at startup and responds in the allowed servers below.
            </span>
          </span>
        </label>

        <div>
          <label className="block text-xs font-medium text-gray-700 mb-1">Bot token</label>
          <input
            type="password"
            autoComplete="off"
            value={botToken}
            onChange={(e) => {
              setBotToken(e.target.value);
              markDirty();
            }}
            placeholder={data?.bot_token_masked || "Bot token from the Developer Portal"}
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <p className="text-[11px] text-gray-500 mt-0.5">
            {data?.has_bot_token
              ? "Leave empty to keep the current token"
              : "From your Discord app's Bot page (Reset Token to reveal it)"}
          </p>
        </div>

        {missingToken && (
          <p className="text-xs text-amber-700">
            Discord is enabled but no bot token is set — the agent can't connect until one is set
            (here or as a DISCORD_BOT_TOKEN env var).
          </p>
        )}

        <DiscordChannelsEditor
          channels={channels}
          disabled={!enabled}
          onChange={(chs) => {
            setChannels(chs);
            markDirty();
          }}
        />

        <DiscordDMPolicySelect
          value={dmPolicy}
          disabled={!enabled}
          onChange={(v) => {
            setDmPolicy(v);
            markDirty();
          }}
        />

        <div className="flex items-center justify-end gap-3 pt-2 border-t border-gray-100">
          {botToken.trim() ? (
            <span className="text-[11px] text-gray-500">Saving a new token restarts the agent.</span>
          ) : null}
          <button
            type="button"
            onClick={handleSave}
            disabled={!dirty || updateMut.isPending}
            className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? "Saving..." : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}
