import * as React from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Sub2ApiImportSummaryDetails } from "@/features/auth/components/sub2api-import-summary-details";
import { createSub2ApiImportSummaryView, parseSub2ApiImportSummary } from "@/features/auth/model/sub2api-import-summary";

export function useAuthSub2ApiImportSummary() {
  const t = useTranslations("login.sub2api.importSummary");

  return React.useCallback((input: unknown) => {
    const parsed = parseSub2ApiImportSummary(input);
    if (!parsed) return;
    const summary = createSub2ApiImportSummaryView(parsed);
    if (summary.readyCount > 0 && !summary.hasIssues) {
      toast.success(t("ready", { count: summary.readyCount }));
      return;
    }

    const description = React.createElement(Sub2ApiImportSummaryDetails, { summary });
    toast.warning(summary.readyCount > 0 ? t("partial", { count: summary.readyCount }) : t("none"), {
      description,
      duration: Number.POSITIVE_INFINITY,
      closeButton: true,
    });
  }, [t]);
}
