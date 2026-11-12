import { ProviderTestOutcomeValues } from "../../contracts/generated";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";

export function DiscoveryError({ error }: { error: ActionError | null }) {
  const t = useT();
  if (error?.code === "ERR_VALIDATION" && error.details?.no_discovery) {
    return (
      <p className="providers-danger" role="alert">
        {t("providers.noDiscovery")}
      </p>
    );
  }
  const outcome = ProviderTestOutcomeValues.find((value) => value === error?.details?.outcome);
  if (error?.code === "ERR_UNAVAILABLE" && outcome) {
    return (
      <p className="providers-danger" role="alert">
        {t(`providers.outcome.${outcome}`)}
      </p>
    );
  }
  return <ErrorText error={error} />;
}
