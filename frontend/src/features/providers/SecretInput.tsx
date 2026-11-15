import { useId, useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { useT } from "../../lib/i18n";

interface SecretInputProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  hint?: string;
  cleared?: boolean;
  onClear: () => void;
  invalid?: boolean;
  required?: boolean;
}

export function SecretInput({
  label,
  value,
  onChange,
  hint,
  cleared,
  onClear,
  invalid,
  required,
}: SecretInputProps) {
  const t = useT();
  const id = useId();
  const [visible, setVisible] = useState(false);
  return (
    <div className="form-group">
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <div className="providers-input-row">
        <input
          id={id}
          className="input-control"
          type={visible && Boolean(value) ? "text" : "password"}
          autoComplete="new-password"
          spellCheck={false}
          value={value}
          required={required}
          disabled={cleared}
          aria-invalid={invalid}
          placeholder={hint ? t("providers.keepSecret", { hint }) : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
        <Button
          type="button"
          iconOnly
          disabled={!value || cleared}
          title={t(visible && Boolean(value) ? "providers.hideSecret" : "providers.showSecret")}
          aria-label={t(visible && Boolean(value) ? "providers.hideSecret" : "providers.showSecret")}
          aria-pressed={visible && Boolean(value)}
          onClick={() => setVisible(!visible)}
        >
          {visible && Boolean(value) ? <EyeOff size={14} /> : <Eye size={14} />}
        </Button>
        {hint && (
          <Button type="button" aria-pressed={!!cleared} onClick={onClear}>
            {t(cleared ? "providers.undoClear" : "providers.clearSecret")}
          </Button>
        )}
      </div>
      {cleared && <span className="providers-danger">{t("providers.secretWillClear")}</span>}
    </div>
  );
}
