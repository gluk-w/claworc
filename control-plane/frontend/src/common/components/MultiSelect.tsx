import Select, { type Props as SelectProps, type StylesConfig } from "react-select";

export interface MultiSelectOption {
  value: number;
  label: string;
}

const multiSelectStyles: StylesConfig<MultiSelectOption, true> = {
  control: (base, state) => ({
    ...base,
    minHeight: "unset",
    borderColor: state.isFocused ? "#3b82f6" : "#d1d5db",
    borderRadius: "0.375rem",
    fontSize: "0.875rem",
    boxShadow: state.isFocused ? "0 0 0 2px rgba(59,130,246,0.3)" : "none",
    "&:hover": { borderColor: state.isFocused ? "#3b82f6" : "#9ca3af" },
  }),
  valueContainer: (base) => ({ ...base, padding: "2px 8px" }),
  input: (base) => ({ ...base, fontSize: "0.875rem", margin: 0, padding: 0 }),
  placeholder: (base) => ({ ...base, color: "#9ca3af", fontSize: "0.875rem" }),
  multiValue: (base) => ({
    ...base,
    backgroundColor: "#eff6ff",
    borderRadius: "0.25rem",
  }),
  multiValueLabel: (base) => ({
    ...base,
    color: "#1d4ed8",
    fontSize: "0.75rem",
    padding: "1px 6px",
  }),
  multiValueRemove: (base) => ({
    ...base,
    color: "#93c5fd",
    borderRadius: "0 0.25rem 0.25rem 0",
    ":hover": { backgroundColor: "#dbeafe", color: "#2563eb" },
  }),
  menu: (base) => ({
    ...base,
    border: "1px solid #e5e7eb",
    borderRadius: "0.375rem",
    boxShadow: "0 4px 6px -1px rgba(0,0,0,0.1)",
    fontSize: "0.875rem",
    zIndex: 9999,
  }),
  menuPortal: (base) => ({ ...base, zIndex: 9999 }),
  option: (base, state) => ({
    ...base,
    backgroundColor: state.isFocused ? "#eff6ff" : "white",
    color: "#374151",
    padding: "6px 12px",
    cursor: "pointer",
    ":active": { backgroundColor: "#dbeafe" },
  }),
  noMessage: (base) => ({ ...base, color: "#9ca3af", fontSize: "0.875rem" }),
  indicatorSeparator: () => ({ display: "none" }),
  dropdownIndicator: (base) => ({
    ...base,
    padding: "4px",
    color: "#9ca3af",
    ":hover": { color: "#6b7280" },
  }),
  clearIndicator: (base) => ({
    ...base,
    padding: "4px",
    color: "#9ca3af",
    ":hover": { color: "#6b7280" },
  }),
};

type MultiSelectProps = Omit<
  SelectProps<MultiSelectOption, true>,
  "isMulti" | "styles" | "menuPortalTarget"
>;

export default function MultiSelect(props: MultiSelectProps) {
  return (
    <Select<MultiSelectOption, true>
      isMulti
      styles={multiSelectStyles}
      menuPortalTarget={document.body}
      {...props}
    />
  );
}
