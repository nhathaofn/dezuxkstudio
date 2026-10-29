import React from "react";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary";
  size?: "sm" | "md";
}

export const Button: React.FC<ButtonProps> = ({
  variant = "secondary",
  size = "sm",
  className = "",
  children,
  ...props
}) => {
  return (
    <button
      className={`ds-button ds-button--${variant} ds-button--${size} ${className}`}
      {...props}
    >
      {children}
    </button>
  );
};
