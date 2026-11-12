"use client";

import { useTranslations } from "next-intl";

import type { Sub2ApiImportSummaryView } from "@/features/auth/model/sub2api-import-summary";

export function Sub2ApiImportSummaryDetails({ summary }: { summary: Sub2ApiImportSummaryView }) {
  const t = useTranslations("login.sub2api.importSummary");
  const countEntries = [
    ["total", t("counts.total")],
    ["eligible", t("counts.eligible")],
    ["imported", t("counts.imported")],
    ["reused", t("counts.reused")],
    ["skipped", t("counts.skipped")],
    ["failed", t("counts.failed")],
  ] satisfies Array<readonly [keyof Sub2ApiImportSummaryView["counts"], string]>;

  return (
    <div className="mt-2 w-full min-w-0 text-left text-xs" data-sub2api-import-summary="">
      <dl className="grid grid-cols-3 gap-x-3 gap-y-1.5 rounded-md bg-muted/45 p-2.5">
        {countEntries.map(([key, label]) => (
          <div key={key} className="flex items-baseline justify-between gap-1">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="tabular-nums text-foreground">{summary.counts[key]}</dd>
          </div>
        ))}
      </dl>
      <details className="mt-2">
        <summary className="cursor-pointer font-medium text-foreground">
          {t("unimported", { count: summary.counts.skipped + summary.counts.failed })}
        </summary>
        {summary.issues.length > 0 ? (
          <ul className="mt-2 max-h-44 space-y-2 overflow-y-auto pr-1">
            {summary.issues.map((issue) => (
              <li key={issue.id} className="rounded-md bg-muted/30 p-2">
                <p className="break-words font-medium leading-4 text-foreground" title={issue.label}>{issue.label}</p>
                <p className="mt-1 leading-4 text-muted-foreground">
                  {t(`status.${issue.status}`)} · {t(`reasons.${issue.reason}`)}
                </p>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mt-2 leading-4 text-muted-foreground">{t("noItemDetails")}</p>
        )}
      </details>
      {summary.readyCount > 0 ? <p className="mt-2 leading-4 text-muted-foreground">{t("manualSelection")}</p> : null}
    </div>
  );
}
