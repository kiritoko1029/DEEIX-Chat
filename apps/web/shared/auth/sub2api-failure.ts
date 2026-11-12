export type Sub2ApiBootstrapFailure =
  | "invalid-link" | "exchange-failed" | "two-factor-failed"
  | "session-binding" | "token-rejected" | "identity-mismatch"
  | "user-inactive" | "key-access-denied" | "no-key"
  | "service-disabled" | "gateway-not-allowed" | "upstream-unavailable"
  | "rate-limited" | "provider-unavailable";

// Only fixed, local categories can reach the UI. Never render upstream text,
// URLs, arbitrary details or a response body containing credentials.
export function classifySub2ApiFailure(errorCode: unknown, details: unknown, status?: number): Sub2ApiBootstrapFailure {
  if (status === 429) return "rate-limited";
  switch (errorCode) {
    case "auth.sub2api_disabled":
    case "personal_provider.disabled": return "service-disabled";
    case "auth.sub2api_no_usable_key": return "no-key";
    case "auth.sub2api_import_failed": return "provider-unavailable";
    case "auth.sub2api_gateway_not_allowed": return "gateway-not-allowed";
    case "auth.sub2api_unavailable": return "upstream-unavailable";
    case "auth.sub2api_invalid_request": return "invalid-link";
    case "auth.sub2api_rejected": {
      if (typeof details !== "object" || details === null || Array.isArray(details)) return "token-rejected";
      const reason = "reason" in details ? details.reason : undefined;
      switch (reason) {
        case "session_binding_mismatch": return "session-binding";
        case "user_id_mismatch":
        case "key_owner_mismatch": return "identity-mismatch";
        case "user_inactive": return "user-inactive";
      }
      const stage = "stage" in details ? details.stage : undefined;
      return stage === "keys" ? "key-access-denied" : "token-rejected";
    }
  }
  if (typeof errorCode === "string" && errorCode.startsWith("personal_provider.")) return "provider-unavailable";
  return "exchange-failed";
}

export const sub2ApiFailureMessageKeys: Record<Sub2ApiBootstrapFailure, string> = {
  "invalid-link": "invalidLink",
  "exchange-failed": "exchangeFailed",
  "two-factor-failed": "twoFactorFailed",
  "session-binding": "sessionBinding",
  "token-rejected": "tokenRejected",
  "identity-mismatch": "identityMismatch",
  "user-inactive": "userInactive",
  "key-access-denied": "keyAccessDenied",
  "no-key": "noUsableKey",
  "service-disabled": "serviceDisabled",
  "gateway-not-allowed": "gatewayNotAllowed",
  "upstream-unavailable": "upstreamUnavailable",
  "rate-limited": "rateLimited",
  "provider-unavailable": "providerUnavailable",
};
