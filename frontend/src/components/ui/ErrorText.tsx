import React from "react";
import { describeError, useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";

/** Renders a translated ActionError, or nothing. */
export const ErrorText: React.FC<{ error: ActionError | null }> = ({ error }) => {
  const t = useT();
  if (!error) return null;
  return (
    <div className="alert-box" role="alert">
      {describeError(t, error)}
    </div>
  );
};
