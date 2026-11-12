import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { X } from "lucide-react";
import { useT } from "../../lib/i18n";
import { Button } from "./Button";
import "./dialog.css";

/** Native modal isolation plus explicit keyboard cycling and focus restoration. */
export function Dialog({
  title,
  children,
  footer,
  onClose,
  dirty = false,
  busy = false,
}: {
  title: string;
  children: ReactNode;
  footer?: ReactNode | ((requestClose: () => void) => ReactNode);
  onClose: () => void;
  dirty?: boolean;
  busy?: boolean;
}) {
  const t = useT();
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  useEffect(() => {
    const dialog = ref.current;
    const previous = document.activeElement;
    dialog?.showModal();
    return () => {
      dialog?.close();
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, []);
  const requestClose = () => {
    if (busy) return;
    if (dirty) setConfirmDiscard(true);
    else onClose();
  };
  return (
    <dialog
      ref={ref}
      className="ui-dialog"
      aria-modal="true"
      aria-labelledby={id}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        requestClose();
      }}
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const rect = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < rect.left ||
          event.clientX > rect.right ||
          event.clientY < rect.top ||
          event.clientY > rect.bottom
        )
          requestClose();
      }}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        event.stopPropagation();
        const items = Array.from(
          event.currentTarget.querySelectorAll<HTMLElement>(
            'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex="0"]'
          )
        ).filter((item) => !item.closest("fieldset:disabled") && item.getClientRects().length > 0);
        const first = items[0];
        const last = items[items.length - 1];
        if (!first) {
          event.preventDefault();
          return;
        }
        if (
          event.shiftKey &&
          (document.activeElement === first || document.activeElement === ref.current)
        ) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }}
    >
      <header className="ui-dialog-header">
        <h2 id={id}>{title}</h2>
        <Button
          type="button"
          iconOnly
          title={t("dialog.close")}
          aria-label={t("dialog.close")}
          disabled={busy}
          onClick={requestClose}
        >
          <X size={16} />
        </Button>
      </header>
      <div className="ui-dialog-body">{children}</div>
      {footer && (
        <footer className="ui-dialog-footer">
          {typeof footer === "function" ? footer(requestClose) : footer}
        </footer>
      )}
      {confirmDiscard && (
        <Dialog
          title={t("dialog.discardTitle")}
          onClose={() => setConfirmDiscard(false)}
          footer={
            <>
              <Button onClick={() => setConfirmDiscard(false)}>{t("dialog.keepEditing")}</Button>
              <Button variant="primary" onClick={onClose}>
                {t("dialog.discard")}
              </Button>
            </>
          }
        >
          <p>{t("dialog.discardBody")}</p>
        </Dialog>
      )}
    </dialog>
  );
}
