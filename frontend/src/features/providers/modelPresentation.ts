import type { ProviderModel } from "../../contracts/generated";

export function filterModels(models: ProviderModel[], search: string) {
  const query = search.trim().toLocaleLowerCase();
  return models.filter((model) =>
    `${model.model_id} ${model.display_name}`.toLocaleLowerCase().includes(query)
  );
}
export function enabledCounts(models: ProviderModel[]) {
  return { enabled: models.filter((model) => model.enabled).length, total: models.length };
}
export function factsSummary(
  facts: ProviderModel["facts"],
  labels: { seconds: string; audio: string; tokens: (count: string) => string }
) {
  return [
    facts.durations.length ? `${facts.durations.join(" / ")} ${labels.seconds}` : "",
    facts.resolutions.join(", "),
    facts.audio ? labels.audio : "",
    facts.max_output_tokens
      ? labels.tokens(
          facts.max_output_tokens >= 1000 && facts.max_output_tokens % 1000 === 0
            ? `${facts.max_output_tokens / 1000}k`
            : String(facts.max_output_tokens)
        )
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}
