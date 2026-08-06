import { useEffect, useState } from "react";
import { useInstanceSlack, useUpdateInstanceSlack } from "@common/hooks/useSlack";
import type { InstanceSlackUpdatePayload, SlackChannel } from "@common/types/slack";
import { successToast, errorToast, infoToast } from "@common/utils/toast";
import SlackChannelsEditor, { SlackDMPolicySelect } from "@common/components/SlackChannelsEditor";

interface Props {
  instanceId: number;
}

/**
 * Per-agent Slack connection card (settings tab). Bot/app tokens are stored
 * as encrypted env vars (SLACK_BOT_TOKEN / SLACK_APP_TOKEN) — changing them
 * restarts the agent container; channel/DM changes apply live.
 */
export default function SlackSection({ instanceId }: Props) {
  const { data, isLoading, error } = useInstanceSlack(instanceId);
  const updateMut = useUpdateInstanceSlack(instanceId);

  const [enabled, setEnabled] = useState(false);
  const [channels, setChannels] = useState<SlackChannel[]>([]);
  const [dmPolicy, setDmPolicy] = useState("");
  const [botToken, setBotToken] = useState("");
  const [appToken, setAppToken] = useState("");
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (!data || dirty) return;
    setEnabled(data.enabled);
    setChannels(data.channels);
    setDmPolicy(data.dm_policy ?? "");
  }, [data, dirty]);

  const markDirty = () => setDirty(true);

  async function handleSave() {
    const payload: InstanceSlackUpdatePayload = {
      enabled,
      channels: channels.filter((c) => c.id.trim() !== ""),
      dm_policy: dmPolicy,
    };
    // Tokens are only sent when the user typed one — an empty field means
    // "keep the current token" (remove via the Environment Variables card).
    if (botToken.trim()) payload.bot_token = botToken.trim();
    if (appToken.trim()) payload.app_token = appToken.trim();

    try {
      const res = await updateMut.mutateAsync(payload);
      setBotToken("");
      setAppToken("");
      setDirty(false);
      if (res.restarting) {
        infoToast("Slack settings saved", "Restarting the agent to apply the new tokens…");
      } else {
        successToast("Slack settings saved");
      }
    } catch (e: any) {
      errorToast("Failed to save Slack settings", e);
    }
  }

  if (isLoading) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900">Slack</h3>
        <p className="text-xs text-gray-500 mt-2">Loading…</p>
      </div>
    );
  }
  if (error) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900">Slack</h3>
        <p className="text-xs text-red-600 mt-2">Failed to load Slack config.</p>
      </div>
    );
  }

  const missingTokens =
    enabled && !((data?.has_bot_token || botToken.trim()) && (data?.has_app_token || appToken.trim()));

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <h3 className="text-sm font-medium text-gray-900 mb-1">Slack</h3>
      <p className="text-xs text-gray-500 mb-4">
        Connect this agent to a Slack workspace via a Socket Mode app. Tokens are stored encrypted
        and never written into the agent's config file.
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
            <span className="block text-sm text-gray-900">Enable Slack</span>
            <span className="block text-xs text-gray-500">
              The agent joins Slack at startup and responds in the allowed channels below.
            </span>
          </span>
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <TokenInput
            label="Bot token"
            placeholder={data?.bot_token_masked || "xoxb-…"}
            value={botToken}
            onChange={(v) => {
              setBotToken(v);
              markDirty();
            }}
            hint={data?.has_bot_token ? "Leave empty to keep the current token" : "From your Slack app's OAuth & Permissions page"}
          />
          <TokenInput
            label="App token"
            placeholder={data?.app_token_masked || "xapp-…"}
            value={appToken}
            onChange={(v) => {
              setAppToken(v);
              markDirty();
            }}
            hint={data?.has_app_token ? "Leave empty to keep the current token" : "App-level token with connections:write (Socket Mode)"}
          />
        </div>

        {missingTokens && (
          <p className="text-xs text-amber-700">
            Slack is enabled but {data?.has_bot_token || botToken.trim() ? "the app token" : data?.has_app_token || appToken.trim() ? "the bot token" : "both tokens"} are missing — the agent can't connect until they are set (here or as
            SLACK_BOT_TOKEN / SLACK_APP_TOKEN env vars).
          </p>
        )}

        <SlackChannelsEditor
          channels={channels}
          disabled={!enabled}
          onChange={(chs) => {
            setChannels(chs);
            markDirty();
          }}
        />

        <SlackDMPolicySelect
          value={dmPolicy}
          disabled={!enabled}
          onChange={(v) => {
            setDmPolicy(v);
            markDirty();
          }}
        />

        <div className="flex items-center justify-end gap-3 pt-2 border-t border-gray-100">
          {botToken.trim() || appToken.trim() ? (
            <span className="text-[11px] text-gray-500">Saving new tokens restarts the agent.</span>
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

function TokenInput({
  label,
  placeholder,
  value,
  onChange,
  hint,
}: {
  label: string;
  placeholder: string;
  value: string;
  onChange: (v: string) => void;
  hint?: string;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-gray-700 mb-1">{label}</label>
      <input
        type="password"
        autoComplete="off"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
      {hint && <p className="text-[11px] text-gray-500 mt-0.5">{hint}</p>}
    </div>
  );
}
