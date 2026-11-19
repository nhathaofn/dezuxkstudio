import React from "react";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost";
  size?: "sm" | "md";
  iconOnly?: boolean;
}

export const Button: React.FC<ButtonProps> = ({
  variant = "secondary",
  size = "sm",
  iconOnly = false,
  className = "",
  type = "button",
  children,
  ...props
}) => {
  return (
    <button
      type={type}
      className={`ds-button ds-button--${variant} ds-button--${size}${iconOnly ? " ds-button--icon" : ""}${className ? ` ${className}` : ""}`}
      {...props}
    >
      {children}
    </button>
  );
};
