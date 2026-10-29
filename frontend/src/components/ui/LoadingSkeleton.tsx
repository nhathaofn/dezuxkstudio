import React from "react";
import { useT } from "../../lib/i18n";

export const LoadingSkeleton: React.FC = () => {
  const t = useT();
  return <div className="system-card system-card--loading">{t("app.loading")}</div>;
};
