import type { LoginData, Sub2ApiPasswordLoginOptions, Sub2ApiProtocolSelection } from "../../../shared/api/auth-types.ts";
import { classifySub2ApiFailure, type Sub2ApiBootstrapFailure } from "../../../shared/auth/sub2api-failure.ts";
import { parseSub2ApiProtocolSelection } from "../../../shared/auth/sub2api-protocol.ts";
import { isRecord } from "../../../shared/lib/type-guards.ts";

export const SUB2API_PASSWORD_FLOW_TTL_MS = 5 * 60 * 1000;

export type Sub2ApiPasswordErrorLike = {
  status: number;
  errorCode?: string;
  details?: unknown;
};

export type Sub2ApiRemoteTwoFactor = {
  stage: "remote_2fa";
  flowID: string;
  expiresAt: string;
  expiresAtMs: number;
  maskedEmail: string;
};

export type Sub2ApiPasswordProgress =
  | Sub2ApiRemoteTwoFactor
  | { stage: "local_2fa" | "authenticated"; login: LoginData };

export type Sub2ApiPasswordProtocolRequired = {
  flowID: string;
  expiresAt: string;
  expiresAtMs: number;
  selection: Sub2ApiProtocolSelection;
};

export type Sub2ApiPasswordFailure =
  | { legacy: false; reason: "disabled" | "unavailable" | "invalid-request" | "invalid-credentials" | "captcha-failed" | "remote-two-factor-invalid" | "flow-expired" | "flow-busy" | "rate-limited" }
  | { legacy: true; reason: Sub2ApiBootstrapFailure };

function hasOnlyKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  return Object.keys(value).every((key) => keys.includes(key));
}

function readBoundedString(value: unknown, maxLength: number, allowEmpty = true): string | null {
  if (typeof value !== "string" || (!allowEmpty && !value) || value.length > maxLength || /[\p{Cc}\p{Cf}]/u.test(value)) return null;
  return value;
}

export function isSub2ApiPasswordFlowID(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9_-]{43}$/.test(value);
}

function parseExpiry(value: unknown): { expiresAt: string; expiresAtMs: number } | null {
  if (typeof value !== "string" || value.length > 64 || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) return null;
  const expiresAtMs = Date.parse(value);
  return Number.isFinite(expiresAtMs) ? { expiresAt: value, expiresAtMs } : null;
}

function parseFallbackURL(value: unknown): string | null {
  if (typeof value !== "string" || value.length > 2048) return null;
  if (!value) return "";
  try {
    const url = new URL(value);
    if ((url.protocol !== "https:" && url.protocol !== "http:") || url.username || url.password) return null;
    return url.toString();
  } catch {
    return null;
  }
}

export function parseSub2ApiPasswordLoginOptions(value: unknown): Sub2ApiPasswordLoginOptions | null {
  if (!isRecord(value)) return null;
  if (typeof value.enabled !== "boolean" || typeof value.passwordLoginAvailable !== "boolean" || value.browserOnly !== true || typeof value.remoteTotpEnabled !== "boolean") return null;
  const unavailableReason = value.unavailableReason === null ? null : readBoundedString(value.unavailableReason, 128);
  const upstreamVersion = readBoundedString(value.upstreamVersion, 64);
  const fallbackURL = parseFallbackURL(value.fallbackURL);
  if ((value.unavailableReason !== null && unavailableReason === null) || upstreamVersion === null || fallbackURL === null || !isRecord(value.captcha)) return null;
  const provider = value.captcha.provider;
  const siteKey = readBoundedString(value.captcha.siteKey, 256);
  if ((provider !== "none" && provider !== "turnstile") || siteKey === null || (provider === "turnstile" && !siteKey)) return null;
  if (value.enabled && !fallbackURL) return null;
  return {
    enabled: value.enabled,
    passwordLoginAvailable: value.passwordLoginAvailable,
    browserOnly: true,
    unavailableReason,
    upstreamVersion,
    remoteTotpEnabled: value.remoteTotpEnabled,
    captcha: { provider, siteKey },
    fallbackURL,
  };
}

function parseRemoteTwoFactor(value: unknown): Sub2ApiRemoteTwoFactor | null {
  if (!isRecord(value) || value.stage !== "remote_2fa" || !hasOnlyKeys(value, ["stage", "flowID", "expiresAt", "maskedEmail"])) return null;
  if (!isSub2ApiPasswordFlowID(value.flowID)) return null;
  const expiry = parseExpiry(value.expiresAt);
  const maskedEmail = readBoundedString(value.maskedEmail, 128);
  if (!expiry || maskedEmail === null) return null;
  return { stage: "remote_2fa", flowID: value.flowID, ...expiry, maskedEmail };
}

function parseLogin(value: unknown, stage: "local_2fa" | "authenticated"): LoginData | null {
  if (!isRecord(value) || !isRecord(value.user)) return null;
  if (stage === "local_2fa") {
    if (value.twoFactorRequired !== true || readBoundedString(value.twoFactorChallengeToken, 16384, false) === null) return null;
  } else if (value.twoFactorRequired === true || readBoundedString(value.accessToken, 16384, false) === null || readBoundedString(value.sessionID, 256, false) === null) {
    return null;
  }
  // Type assertion: the stage guard rejects remote-only objects; session commit validates the required credentials again.
  return value as LoginData;
}

export function parseSub2ApiPasswordProgress(value: unknown): Sub2ApiPasswordProgress | null {
  if (!isRecord(value)) return null;
  if (value.stage === "remote_2fa") return parseRemoteTwoFactor(value);
  if ((value.stage === "local_2fa" || value.stage === "authenticated") && hasOnlyKeys(value, ["stage", "login"])) {
    const login = parseLogin(value.login, value.stage);
    return login ? { stage: value.stage, login } : null;
  }
  return null;
}

export function parseSub2ApiPasswordProtocolRequired(error: Sub2ApiPasswordErrorLike): Sub2ApiPasswordProtocolRequired | null {
  const details = error.details;
  if (error.status !== 409 || error.errorCode !== "auth.sub2api_protocol_required" || !isRecord(details) || details.stage !== "protocol_selection") return null;
  if (!hasOnlyKeys(details, ["stage", "flowID", "expiresAt", "protocolSelection"]) || !isSub2ApiPasswordFlowID(details.flowID)) return null;
  const expiry = parseExpiry(details.expiresAt);
  const selection = parseSub2ApiProtocolSelection(error.status, error.errorCode, details);
  return expiry && selection ? { flowID: details.flowID, ...expiry, selection } : null;
}

export function parseSub2ApiRemoteTwoFactorRetry(error: Sub2ApiPasswordErrorLike): Sub2ApiRemoteTwoFactor | null {
  if (error.status !== 400 || error.errorCode !== "auth.sub2api_remote_two_factor_invalid") return null;
  return parseRemoteTwoFactor(error.details);
}

export function readSub2ApiPasswordFlowID(value: unknown): string | null {
  if (!isRecord(value)) return null;
  if (isSub2ApiPasswordFlowID(value.flowID)) return value.flowID;
  return isRecord(value.details) && isSub2ApiPasswordFlowID(value.details.flowID) ? value.details.flowID : null;
}

export function isSub2ApiPasswordFlowExpiryValid(expiresAtMs: number, now = Date.now()): boolean {
  return expiresAtMs > now && expiresAtMs <= now + SUB2API_PASSWORD_FLOW_TTL_MS;
}

export function classifySub2ApiPasswordFailure(error: unknown): Sub2ApiPasswordFailure {
  if (!isRecord(error) || typeof error.status !== "number") return { legacy: false, reason: "unavailable" };
  const status = error.status;
  const errorCode = typeof error.errorCode === "string" ? error.errorCode : undefined;
  if (status === 429 || errorCode === "auth.sub2api_login_rate_limited") return { legacy: false, reason: "rate-limited" };
  const known: Record<string, Sub2ApiPasswordFailure> = {
    "auth.sub2api_password_disabled": { legacy: false, reason: "disabled" },
    "auth.sub2api_password_unavailable": { legacy: false, reason: "unavailable" },
    "auth.sub2api_password_invalid_request": { legacy: false, reason: "invalid-request" },
    "auth.sub2api_invalid_credentials": { legacy: false, reason: "invalid-credentials" },
    "auth.sub2api_captcha_failed": { legacy: false, reason: "captcha-failed" },
    "auth.sub2api_remote_two_factor_invalid": { legacy: false, reason: "remote-two-factor-invalid" },
    "auth.sub2api_login_flow_expired": { legacy: false, reason: "flow-expired" },
    "auth.sub2api_login_flow_busy": { legacy: false, reason: "flow-busy" },
  };
  const knownFailure = errorCode ? known[errorCode] : undefined;
  if (knownFailure) return knownFailure;
  return { legacy: true, reason: classifySub2ApiFailure(errorCode, error.details, status) };
}
