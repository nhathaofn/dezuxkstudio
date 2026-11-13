import React from "react";
import { FolderOpen } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { getBridge, isDesktop } from "../../lib/bridge";
import { useT } from "../../lib/i18n";

interface OutputDirFieldProps {
  id: string;
  value: string;
  onChange: (dir: string) => void;
  disabled?: boolean;
}

export const OutputDirField: React.FC<OutputDirFieldProps> = ({
  id,
  value,
  onChange,
  disabled,
}) => {
  const t = useT();
  const canBrowse = isDesktop() && Boolean(getBridge()?.BrowseDirectory);

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (!bridge?.BrowseDirectory) return;
    try {
      const selected = await bridge.BrowseDirectory(t("watermark.outputDir"));
      if (selected) {
        onChange(selected);
      }
    } catch (err) {
      console.error("BrowseDirectory failed:", err);
    }
  };

  return (
    <div className="form-group">
      <label className="field-label" htmlFor={id}>
        {t("watermark.outputDir")}
      </label>
      <div className="watermark-dir-row">
        <input
          id={id}
          type="text"
          className="input-control input-control--mono"
          placeholder={t("mediagen.field.outputDirPlaceholder")}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
        />
        {canBrowse && (
          <Button
            variant="ghost"
            size="sm"
            onClick={handleBrowse}
            title={t("watermark.browseFolder")}
            aria-label={t("watermark.browseFolder")}
            disabled={disabled}
          >
            <FolderOpen size={14} />
          </Button>
        )}
      </div>
    </div>
  );
};
