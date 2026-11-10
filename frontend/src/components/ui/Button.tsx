import React from "react";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary";
  size?: "sm" | "md";
  iconOnly?: boolean;
}

export const Button: React.FC<ButtonProps> = ({
  variant = "secondary",
  size = "sm",
  iconOnly = false,
  className = "",
  children,
  ...props
}) => {
  return (
    <button
      className={`ds-button ds-button--${variant} ds-button--${size}${iconOnly ? " ds-button--icon" : ""}${className ? ` ${className}` : ""}`}
      {...props}
    >
      {children}
    </button>
  );
};
