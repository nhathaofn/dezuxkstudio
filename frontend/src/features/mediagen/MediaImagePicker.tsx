import React, { useRef, useState } from "react";
import { Image as ImageIcon, Plus, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { getBridge, isDesktop } from "../../lib/bridge";
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
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [dragActive, setDragActive] = useState(false);

  const handleBrowse = async () => {
    if (disabled) return;
    const bridge = getBridge();
    if (isDesktop() && bridge) {
      try {
        if (multiple && bridge.BrowseImages) {
          const picked = await bridge.BrowseImages(label);
          if (picked && picked.length > 0) {
            onAdd(picked.map(fromLocalPath));
          }
          return;
        }
        if (bridge.BrowseImage) {
          const picked = await bridge.BrowseImage(label);
          if (picked) {
            onAdd([fromLocalPath(picked)]);
          }
          return;
        }
      } catch (err) {
        console.error("Native image picker failed:", err);
      }
    }
    fileInputRef.current?.click();
  };

  const applyFileList = (list: FileList | null) => {
    if (!list || list.length === 0) return;
    const picked = Array.from(list).map(fromBrowserFile);
    onAdd(multiple ? picked : [picked[0]]);
  };

  return (
    <div className="form-group">
      <div className="mediagen-picker-header">
        <label className="field-label watermark-label-with-icon">
          <ImageIcon size={13} />
          <span>{label}</span>
        </label>
        <Button variant="ghost" size="sm" onClick={handleBrowse} disabled={disabled}>
          <Plus size={13} />
          {t("mediagen.action.addImage")}
        </Button>
      </div>

      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        multiple={multiple}
        className="mediagen-hidden-input"
        onChange={(e) => {
          applyFileList(e.target.files);
          e.target.value = "";
        }}
      />

      {items.length > 0 ? (
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
      ) : (
        <div
          className={`mediagen-drop-hint${dragActive ? " mediagen-drop-hint--active" : ""}`}
          onDragOver={(e) => {
            e.preventDefault();
            if (!disabled) setDragActive(true);
          }}
          onDragLeave={() => setDragActive(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragActive(false);
            if (!disabled) applyFileList(e.dataTransfer.files);
          }}
          onClick={handleBrowse}
          role="button"
          tabIndex={0}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              void handleBrowse();
            }
          }}
        >
          {t("mediagen.hint.dropOrPickImage")}
        </div>
      )}
    </div>
  );
};
