import React from "react";
import type { LucideIcon } from "lucide-react";

interface CardProps {
  icon: LucideIcon;
  title: string;
  subtitle?: string;
  actions?: React.ReactNode;
  wide?: boolean;
  className?: string;
  children?: React.ReactNode;
}

/** Standard content card: icon + title row, optional subtitle and header actions. */
export const Card: React.FC<CardProps> = ({
  icon: Icon,
  title,
  subtitle,
  actions,
  wide,
  className,
  children,
}) => (
  <section
    className={`system-card${wide ? " system-card--wide" : ""}${className ? ` ${className}` : ""}`}
  >
    <div className="card-header-row">
      <div className="card-title">
        <Icon size={18} className="card-title__icon" />
        <h2>{title}</h2>
      </div>
      {actions && <div className="card-actions-row">{actions}</div>}
    </div>
    {subtitle && <p className="subtitle">{subtitle}</p>}
    {children}
  </section>
);
