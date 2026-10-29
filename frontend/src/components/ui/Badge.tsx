import React from "react";

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  children: React.ReactNode;
}

export const Badge: React.FC<BadgeProps> = ({ children, className = "", ...props }) => {
  return (
    <span className={`ds-badge ${className}`} {...props}>
      {children}
    </span>
  );
};
