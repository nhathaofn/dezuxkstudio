import { AudioLines, Image, LayoutGrid, MessageSquare, Video } from "lucide-react";
import type { ProviderCapability } from "../../contracts/generated";

export function CapabilityIcon({
  value,
  size = 14,
}: {
  value: "all" | ProviderCapability;
  size?: number;
}) {
  const Icon = { all: LayoutGrid, llm: MessageSquare, image: Image, video: Video, tts: AudioLines }[
    value
  ];
  return <Icon size={size} aria-hidden="true" />;
}
