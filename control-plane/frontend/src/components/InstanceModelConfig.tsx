// TODO: This component is intended for future per-instance model configuration.
// It allows toggling global default models and adding extra models per instance.
// Wire it into the instance detail/edit page when per-instance model overrides are implemented.
import ModelListEditor from "./ModelListEditor";

interface InstanceModelConfigProps {
  /** Global default models list */
  defaultModels: string[];
  /** Models disabled by this instance (subset of defaultModels) */
  disabledDefaults: string[];
  /** Extra models added for this instance */
  extraModels: string[];
  onDisabledChange: (disabled: string[]) => void;
  onExtraChange: (extra: string[]) => void;
}

export default function InstanceModelConfig({
  defaultModels,
  disabledDefaults,
  extraModels,
  onDisabledChange,
  onExtraChange,
}: InstanceModelConfigProps) {
  const toggleDefault = (model: string) => {
    if (disabledDefaults.includes(model)) {
      onDisabledChange(disabledDefaults.filter((m) => m !== model));
    } else {
      onDisabledChange([...disabledDefaults, model]);
    }
  };

  return (
    <div className="space-y-4">
      {defaultModels.length > 0 && (
        <div>
          <label className="block text-xs text-gray-500 mb-2">
            Default Models
          </label>
          <div className="space-y-1.5">
            {defaultModels.map((model) => (
              <label key={model} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={!disabledDefaults.includes(model)}
                  onChange={() => toggleDefault(model)}
                  className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                />
                <span className="font-mono text-gray-700">{model}</span>
              </label>
            ))}
          </div>
        </div>
      )}

      <div>
        <label className="block text-xs text-gray-500 mb-2">
          Additional Models
        </label>
        <ModelListEditor models={extraModels} onChange={onExtraChange} />
      </div>
    </div>
  );
}
