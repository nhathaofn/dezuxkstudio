import type { ButtonHTMLAttributes } from "react";
import "./switch.css";

export function Switch({
  checked,
  onCheckedChange,
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange"> & {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <button
      {...props}
      type="button"
      role="switch"
      aria-checked={checked}
      className="ui-switch"
      onClick={() => onCheckedChange(!checked)}
    >
      <span />
    </button>
  );
}
