import { useEffect, useId, useState } from "react";
import { Copy, ExternalLink } from "lucide-react";
import type { AgentLoginPrompt, AgentProviderInfo } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { useFlash } from "../../lib/hooks/useFlash";
import { openExternal } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { COPY_FEEDBACK_MS, useAgentStore } from "./useAgentStore";

/**
 * Finishes a sign-in that the CLI started: shows the page to open, the one-time code to type there,
 * and (when the CLI waits for it) a field for the code the page shows afterwards.
 */
export function LoginPromptDialog({
  provider,
  prompt,
}: {
  provider: AgentProviderInfo;
  prompt: AgentLoginPrompt;
}) {
  const t = useT();
  const id = useId();
  const submit = useAgentStore((s) => s.submitLoginCode);
  const cancel = useAgentStore((s) => s.cancelLogin);
  const [pasted, setPasted] = useState("");
  const [sent, setSent] = useState(false);
  const [copiedCode, flashCopiedCode] = useFlash(COPY_FEEDBACK_MS);
  const [copiedLink, flashCopiedLink] = useFlash(COPY_FEEDBACK_MS);

  // The CLI opens the page itself for some providers; for the others the app does, once per page.
  useEffect(() => {
    if (prompt.open_url && prompt.url) openExternal(prompt.url);
  }, [prompt.open_url, prompt.url]);

  const copy = async (text: string, flash: () => void) => {
    try {
      await navigator.clipboard.writeText(text);
      flash();
    } catch {
      /* the text stays selectable */
    }
  };

  return (
    <Dialog
      title={t("agent.login.title", { name: provider.name })}
      size="sm"
      onClose={() => void cancel(provider.id)}
      footer={
        <>
          <Button type="button" onClick={() => void cancel(provider.id)}>
            {t("agent.login.cancel")}
          </Button>
          {prompt.paste_code && (
            <Button
              type="submit"
              form={`${id}-form`}
              variant="primary"
              disabled={!pasted.trim() || sent}
            >
              {t(sent ? "agent.login.confirming" : "agent.login.confirm")}
            </Button>
          )}
        </>
      }
    >
      <form
        id={`${id}-form`}
        className="agent-login"
        onSubmit={(event) => {
          event.preventDefault();
          if (!pasted.trim() || sent) return;
          setSent(true);
          void submit(provider.id, pasted.trim());
        }}
      >
        {prompt.url ? (
          <section className="agent-login__step">
            <p>{t("agent.login.openPage")}</p>
            <div className="agent-login__row">
              <Button type="button" variant="primary" onClick={() => openExternal(prompt.url)}>
                <ExternalLink size={14} /> {t("agent.login.openButton")}
              </Button>
              <Button
                type="button"
                iconOnly
                title={t("agent.login.copyLink")}
                aria-label={t("agent.login.copyLink")}
                onClick={() => void copy(prompt.url, () => flashCopiedLink(true))}
              >
                <Copy size={14} />
              </Button>
              {copiedLink && <span className="agent-login__copied">{t("agent.login.copied")}</span>}
            </div>
          </section>
        ) : (
          <p role="status">{t("agent.login.waiting")}</p>
        )}

        {prompt.code && (
          <section className="agent-login__step">
            <p>{t("agent.login.enterCode")}</p>
            <div className="agent-login__row">
              <code className="agent-login__code">{prompt.code}</code>
              <Button
                type="button"
                iconOnly
                title={t("agent.login.copyCode")}
                aria-label={t("agent.login.copyCode")}
                onClick={() => void copy(prompt.code, () => flashCopiedCode(true))}
              >
                <Copy size={14} />
              </Button>
              {copiedCode && <span className="agent-login__copied">{t("agent.login.copied")}</span>}
            </div>
          </section>
        )}

        {prompt.paste_code && (
          <section className="agent-login__step">
            <label htmlFor={`${id}-code`} className="agent-login__label">
              {t("agent.login.pasteCode")}
            </label>
            <input
              id={`${id}-code`}
              className="input-control input-control--mono"
              value={pasted}
              autoComplete="off"
              spellCheck={false}
              disabled={sent}
              onChange={(event) => setPasted(event.target.value)}
            />
          </section>
        )}
      </form>
    </Dialog>
  );
}
