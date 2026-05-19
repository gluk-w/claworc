import { forwardRef, type ComponentProps } from "react";

type EditInputProps = ComponentProps<"input"> & {
  onSave: () => void;
  onCancel: () => void;
};

const EditInput = forwardRef<HTMLInputElement, EditInputProps>(({ onSave, onCancel, onKeyDown, ...rest }, ref) => (
  <input
    ref={ref}
    onKeyDown={(e) => {
      if (e.key === "Enter") onSave();
      if (e.key === "Escape") onCancel();
      onKeyDown?.(e);
    }}
    {...rest}
  />
));

EditInput.displayName = "EditInput";

export default EditInput;
