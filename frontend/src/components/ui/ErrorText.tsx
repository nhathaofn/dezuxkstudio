import React from "react";
import { X } from "lucide-react";
import { describeError, useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";

/** Renders a translated ActionError with an optional explicit dismiss button. */
export const ErrorText: React.FC<{
  error: ActionError | null;
  onDismiss?: () => void;
}> = ({ error, onDismiss }) => {
  const t = useT();
  if (!error) return null;
  return (
    <div className="alert-box" role="alert">
      <span>{describeError(t, error)}</span>
      {onDismiss && (
        <button
          type="button"
          className="alert-box__dismiss"
          onClick={onDismiss}
          aria-label={t("dialog.close")}
          title={t("dialog.close")}
        >
          <X size={13} />
        </button>
      )}
    </div>
  );
};
