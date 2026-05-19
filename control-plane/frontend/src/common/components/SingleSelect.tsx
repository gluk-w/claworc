import Select, { type Props as SelectProps, type StylesConfig } from "react-select";

export interface SingleSelectOption {
  value: number;
  label: string;
}

const singleSelectStyles: StylesConfig<SingleSelectOption, false> = {
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
  singleValue: (base) => ({ ...base, color: "#111827", fontSize: "0.875rem" }),
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
    backgroundColor: state.isSelected
      ? "#eff6ff"
      : state.isFocused
        ? "#f9fafb"
        : "white",
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

type SingleSelectProps = Omit<
  SelectProps<SingleSelectOption, false>,
  "isMulti" | "styles" | "menuPortalTarget"
>;

export default function SingleSelect(props: SingleSelectProps) {
  return (
    <Select<SingleSelectOption, false>
      styles={singleSelectStyles}
      menuPortalTarget={document.body}
      {...props}
    />
  );
}
