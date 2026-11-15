import React, { useRef } from "react";
import { Image as ImageIcon, Plus, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { MediaDropZone, type MediaDropZoneHandle } from "../../components/ui/MediaDropZone";
import { useT } from "../../lib/i18n";
import { fromBrowserFile, fromLocalPath, type PickedImage } from "./types";

interface MediaImagePickerProps {
  label: string;
  multiple?: boolean;
  items: PickedImage[];
  onAdd: (items: PickedImage[]) => void;
  onRemove: (index: number) => void;
  disabled?: boolean;
}

export const MediaImagePicker: React.FC<MediaImagePickerProps> = ({
  label,
  multiple = false,
  items,
  onAdd,
  onRemove,
  disabled,
}) => {
  const t = useT();
  const dropZoneRef = useRef<MediaDropZoneHandle | null>(null);

  const handleSelected = (files: File[], paths?: string[]) => {
    if (paths && paths.length > 0) {
      onAdd(paths.map(fromLocalPath));
    } else if (files && files.length > 0) {
      onAdd(files.map(fromBrowserFile));
    }
  };

  return (
    <div className="form-group">
      <div className="mediagen-picker-header">
        <label className="field-label watermark-label-with-icon">
          <ImageIcon size={13} />
          <span>{label}</span>
        </label>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => dropZoneRef.current?.browse()}
          disabled={disabled}
        >
          <Plus size={13} />
          {t("mediagen.action.addImage")}
        </Button>
      </div>

      {items.length > 0 ? (
        <>
          <div className="mediagen-chip-list">
            {items.map((item, idx) => (
              <div key={`${item.localPath || item.label}-${idx}`} className="mediagen-chip">
                <span className="mediagen-chip__name" title={item.localPath || item.label}>
                  {item.label}
                </span>
                <button
                  type="button"
                  className="mediagen-chip__remove"
                  aria-label={t("mediagen.action.removeImage")}
                  onClick={() => onRemove(idx)}
                  disabled={disabled}
                >
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
          {/* Hidden dropzone to retain native and file dialog browsing capability when items exist */}
          <div style={{ display: "none" }}>
            <MediaDropZone
              ref={dropZoneRef}
              label={label}
              multiple={multiple}
              disabled={disabled}
              onSelected={handleSelected}
            />
          </div>
        </>
      ) : (
        <MediaDropZone
          ref={dropZoneRef}
          label={label}
          multiple={multiple}
          disabled={disabled}
          className="mediagen-drop-hint"
          title={t("mediagen.hint.dropOrPickImage")}
          onSelected={handleSelected}
        />
      )}
    </div>
  );
};
