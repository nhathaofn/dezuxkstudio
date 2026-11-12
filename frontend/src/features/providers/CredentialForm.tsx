import { useId, useState } from "react";
import { KeyRound } from "lucide-react";
import type { BuiltinProvider, ProviderCredential } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { ActionError, toActionError } from "../../lib/rtClient";
import { buildCredentialPayload } from "./credentialPayload";
import { fieldLabel } from "./fieldLabel";
import { SecretInput } from "./SecretInput";
import { useProvidersStore } from "./useProvidersStore";

interface CredentialFormProps {
  provider: BuiltinProvider;
  credential?: ProviderCredential;
  onClose: () => void;
  onSaved: () => void;
  disabled: boolean;
}

export function CredentialForm({
  provider,
  credential,
  onClose,
  onSaved,
  disabled,
}: CredentialFormProps) {
  const t = useT();
  const id = useId();
  const create = useProvidersStore((s) => s.createCredential);
  const update = useProvidersStore((s) => s.updateCredential);
  const [name, setName] = useState(credential?.name ?? "");
  const [inputs, setInputs] = useState<Record<string, string>>({ ...credential?.values });
  const [group, setGroup] = useState(() => {
    const index = provider.credential_groups.findIndex((keys) =>
      keys.every((key) => credential?.secret_hints[key])
    );
    return Math.max(0, index);
  });
  const [clearSecrets, setClearSecrets] = useState<string[]>([]);
  const [error, setError] = useState<ActionError | null>(null);
  const [saving, setSaving] = useState(false);
  const [readingFile, setReadingFile] = useState(false);
  const selected = provider.credential_groups[group];
  const grouped = new Set(provider.credential_groups.flat());
  const visibleFields = provider.fields.filter(
    (f) => !f.secret || !selected || !grouped.has(f.key) || selected.includes(f.key)
  );
  const change = (key: string, value: string) => {
    setInputs((previous) => ({ ...previous, [key]: value }));
    setClearSecrets((previous) => previous.filter((item) => item !== key));
    setError(null);
  };
  const clear = (key: string) => {
    setInputs((previous) => ({ ...previous, [key]: "" }));
    setClearSecrets((previous) =>
      previous.includes(key) ? previous.filter((item) => item !== key) : [...previous, key]
    );
  };
  return (
    <Card
      icon={KeyRound}
      title={t(credential ? "providers.editCredential" : "providers.addCredential")}
      subtitle={provider.display_name}
    >
      <form
        className="providers-form"
        onSubmit={async (e) => {
          e.preventDefault();
          if (saving || readingFile || disabled) return;
          setSaving(true);
          setError(null);
          try {
            const payload = buildCredentialPayload(provider, name, inputs, group, clearSecrets);
            if (credential) await update(credential.id, payload);
            else await create(provider.id, payload);
            onSaved();
          } catch (err) {
            setError(toActionError(err));
          } finally {
            setSaving(false);
          }
        }}
      >
        <ErrorText error={error} />
        <fieldset disabled={saving || readingFile || disabled} className="providers-fieldset">
          <div className="form-group">
            <label className="field-label" htmlFor={`${id}-name`}>
              {t("providers.nameOptional")}
            </label>
            <input
              id={`${id}-name`}
              className="input-control"
              value={name}
              aria-invalid={error?.details?.field === "name"}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          {provider.credential_groups.length > 0 && (
            <div
              className="settings-btn-group"
              role="group"
              aria-label={t("providers.credentialType")}
            >
              {provider.credential_groups.map((keys, index) => (
                <Button
                  key={keys.join(",")}
                  type="button"
                  variant={group === index ? "primary" : "secondary"}
                  aria-pressed={group === index}
                  onClick={() => {
                    setGroup(index);
                    setError(null);
                  }}
                >
                  {keys.map((key) => fieldLabel(t, key)).join(t("providers.groupSeparator"))}
                </Button>
              ))}
            </div>
          )}
          {visibleFields.map((field) => {
            const hint = credential?.secret_hints[field.key];
            const invalid = error?.details?.field === field.key;
            // Alternative credential groups are validated as a whole by the server.
            const required =
              field.required && !hint && !selected && !clearSecrets.includes(field.key);
            if (field.kind === "file")
              return (
                <div key={field.key} className="form-group">
                  <label className="field-label" htmlFor={`${id}-${field.key}`}>
                    {fieldLabel(t, field.key)}
                  </label>
                  <input
                    id={`${id}-${field.key}`}
                    type="file"
                    accept=".json"
                    aria-invalid={invalid}
                    onChange={async (e) => {
                      const file = e.target.files?.[0];
                      e.target.value = "";
                      if (!file) return;
                      setReadingFile(true);
                      try {
                        change(field.key, await file.text());
                      } catch {
                        setError(new ActionError("ERR_UNAVAILABLE", "", { field: field.key }));
                      } finally {
                        setReadingFile(false);
                      }
                    }}
                  />
                  <label className="field-label" htmlFor={`${id}-${field.key}-json`}>
                    {t("providers.pasteJson")}
                  </label>
                  <textarea
                    id={`${id}-${field.key}-json`}
                    className="textarea-control"
                    rows={5}
                    autoComplete="off"
                    value={inputs[field.key] ?? ""}
                    aria-invalid={invalid}
                    required={required}
                    placeholder={
                      hint ? t("providers.keepSecret", { hint }) : t("providers.pasteJson")
                    }
                    onChange={(e) => change(field.key, e.target.value)}
                  />
                  {hint && (
                    <Button
                      type="button"
                      aria-pressed={clearSecrets.includes(field.key)}
                      onClick={() => clear(field.key)}
                    >
                      {t(
                        clearSecrets.includes(field.key)
                          ? "providers.undoClear"
                          : "providers.clearSecret"
                      )}
                    </Button>
                  )}
                  {clearSecrets.includes(field.key) && (
                    <span className="providers-danger">{t("providers.secretWillClear")}</span>
                  )}
                </div>
              );
            if (field.secret)
              return (
                <SecretInput
                  key={field.key}
                  label={fieldLabel(t, field.key)}
                  value={inputs[field.key] ?? ""}
                  hint={hint}
                  cleared={clearSecrets.includes(field.key)}
                  invalid={invalid}
                  required={required}
                  onChange={(value) => change(field.key, value)}
                  onClear={() => clear(field.key)}
                />
              );
            return (
              <div key={field.key} className="form-group">
                <label className="field-label" htmlFor={`${id}-${field.key}`}>
                  {fieldLabel(t, field.key)}
                </label>
                <input
                  id={`${id}-${field.key}`}
                  className="input-control"
                  type={field.kind === "url" ? "url" : "text"}
                  value={inputs[field.key] ?? ""}
                  aria-invalid={invalid}
                  required={field.required && !(field.kind === "url" && provider.default_base_url)}
                  placeholder={
                    field.placeholder ||
                    (field.kind === "url" ? provider.default_base_url : undefined)
                  }
                  onChange={(e) => change(field.key, e.target.value)}
                />
              </div>
            );
          })}
        </fieldset>
        <div className="card-actions-row">
          <Button type="submit" variant="primary" disabled={saving || readingFile || disabled}>
            {t(saving ? "providers.saving" : "providers.save")}
          </Button>
          <Button type="button" disabled={saving || readingFile} onClick={onClose}>
            {t("providers.cancel")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
