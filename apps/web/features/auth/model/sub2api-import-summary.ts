import type { Sub2ApiImportItem, Sub2ApiImportSummary } from "../../../shared/api/auth-types.ts";

export type Sub2ApiImportReason =
  | "quota" | "limit" | "probe" | "timeout" | "unsupported" | "two-factor"
  | "inactive" | "expired" | "key-invalid" | "group-invalid" | "gateway"
  | "suspended" | "disabled" | "credential-changed" | "duplicate"
  | "blocked-host" | "feature-disabled" | "other";

export type Sub2ApiImportIssueView = {
  id: string;
  label: string;
  status: "skipped" | "failed";
  reason: Sub2ApiImportReason;
};

export type Sub2ApiImportSummaryView = {
  counts: Pick<Sub2ApiImportSummary, "total" | "eligible" | "imported" | "reused" | "skipped" | "failed">;
  readyCount: number;
  hasIssues: boolean;
  issues: Sub2ApiImportIssueView[];
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readCount(value: Record<string, unknown>, key: string): number | null {
  const count = value[key];
  return typeof count === "number" && Number.isSafeInteger(count) && count >= 0 ? count : null;
}

function readString(value: Record<string, unknown>, key: string): string {
  return typeof value[key] === "string" ? value[key].trim() : "";
}

function parseItem(value: unknown): Sub2ApiImportItem | null {
  if (!isRecord(value)) return null;
  const keyID = value.keyID;
  const status = value.status;
  if (typeof keyID !== "number" || !Number.isSafeInteger(keyID) || keyID <= 0) return null;
  if (status !== "imported" && status !== "reused" && status !== "skipped" && status !== "failed") return null;
  const item: Sub2ApiImportItem = {
    keyID,
    name: readString(value, "name"),
    groupName: readString(value, "groupName"),
    status,
  };
  const providerID = readString(value, "providerID");
  const reason = readString(value, "reason");
  if (providerID) item.providerID = providerID;
  if (reason) item.reason = reason;
  return item;
}

export function parseSub2ApiImportSummary(value: unknown): Sub2ApiImportSummary | null {
  if (!isRecord(value) || !Array.isArray(value.items)) return null;
  const total = readCount(value, "total");
  const eligible = readCount(value, "eligible");
  const imported = readCount(value, "imported");
  const reused = readCount(value, "reused");
  const skipped = readCount(value, "skipped");
  const failed = readCount(value, "failed");
  if (total === null || eligible === null || imported === null || reused === null || skipped === null || failed === null) return null;
  return {
    total,
    eligible,
    imported,
    reused,
    skipped,
    failed,
    items: value.items.flatMap((item) => {
      const parsed = parseItem(item);
      return parsed ? [parsed] : [];
    }),
  };
}

export function readSub2ApiImportSummaryFromDetails(details: unknown): Sub2ApiImportSummary | null {
  return isRecord(details) ? parseSub2ApiImportSummary(details.sub2apiImport) : null;
}

function classifyReason(reason: string | undefined): Sub2ApiImportReason {
  const normalized = reason?.trim().toLowerCase().replace(/-/g, "_") ?? "";
  switch (normalized) {
    case "two_factor_required": return "two-factor";
    case "quota_exhausted": return "quota";
    case "limit_reached": return "limit";
    case "probe_failed": return "probe";
    case "timeout": return "timeout";
    case "unsupported_platform":
    case "unsupported_group": return "unsupported";
    case "inactive": return "inactive";
    case "expired": return "expired";
    case "invalid_key": return "key-invalid";
    case "invalid_group": return "group-invalid";
    case "gateway_not_allowed": return "gateway";
    case "provider_suspended": return "suspended";
    case "provider_disabled": return "disabled";
    case "credential_changed": return "credential-changed";
    case "duplicate": return "duplicate";
    case "blocked_host": return "blocked-host";
    case "feature_disabled": return "feature-disabled";
    default: return "other";
  }
}

export function createSub2ApiImportSummaryView(summary: Sub2ApiImportSummary): Sub2ApiImportSummaryView {
  const issues = summary.items.flatMap((item, index) => {
    if (item.status !== "skipped" && item.status !== "failed") return [];
    const labelParts = [item.name.trim(), item.groupName.trim(), `#${item.keyID}`].filter(Boolean);
    return [{
      id: item.providerID ?? `${item.keyID}-${index}`,
      label: labelParts.join(" · "),
      status: item.status,
      reason: classifyReason(item.reason),
    }];
  });
  const readyCount = summary.imported + summary.reused;
  return {
    counts: {
      total: summary.total,
      eligible: summary.eligible,
      imported: summary.imported,
      reused: summary.reused,
      skipped: summary.skipped,
      failed: summary.failed,
    },
    readyCount,
    hasIssues: summary.skipped > 0 || summary.failed > 0 || issues.length > 0,
    issues,
  };
}
