import { AudioLines, Image, LayoutGrid, MessageSquare, Speech, Video } from "lucide-react";
import type { ProviderCapability } from "../../contracts/generated";

export function CapabilityIcon({
  value,
  size = 14,
}: {
  value: "all" | ProviderCapability;
  size?: number;
}) {
  const Icon = {
    all: LayoutGrid,
    llm: MessageSquare,
    image: Image,
    video: Video,
    tts: AudioLines,
    lipsync: Speech,
  }[value];
  return <Icon size={size} aria-hidden="true" />;
}
