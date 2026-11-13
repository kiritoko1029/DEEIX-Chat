import { cancelSub2ApiPasswordLogin, continueSub2ApiPasswordLogin, loginSub2ApiPassword, verifySub2ApiPasswordTwoFactor } from "@/shared/api/auth";
import type { LoginData, Sub2ApiImportSummary, Sub2ApiPasswordCaptcha, Sub2ApiProtocolSelection } from "@/shared/api/auth-types";
import { ApiError } from "@/shared/api/http-client";
import { authTransition } from "@/shared/auth/auth-transition";
import { subscribeSub2ApiBootstrap } from "@/shared/auth/sub2api-bootstrap";
import { createSub2ApiProtocolExchangeOptions, mergeSub2ApiProtocolExchangeOptions, type Sub2ApiProtocolDecision } from "@/shared/auth/sub2api-protocol";
import { completeNativeSignIn } from "@/shared/platform/desktop-session";
import { writeTwoFactorChallenge } from "@/features/auth/model/login-page";
import { parseSub2ApiImportSummary, readSub2ApiImportSummaryFromDetails } from "@/features/auth/model/sub2api-import-summary";
import { rememberPendingSub2ApiSummary } from "@/features/auth/model/sub2api-pending-summary";
import {
  classifySub2ApiPasswordFailure,
  isSub2ApiPasswordFlowExpiryValid,
  parseSub2ApiPasswordProgress,
  parseSub2ApiPasswordProtocolRequired,
  parseSub2ApiRemoteTwoFactorRetry,
  readSub2ApiPasswordFlowID,
  SUB2API_PASSWORD_FLOW_TTL_MS,
  type Sub2ApiPasswordFailure,
} from "@/features/auth/model/sub2api-password-login";

export type Sub2ApiPasswordOutcome =
  | { status: "remote-2fa"; maskedEmail: string; expiresAt: string; error?: Sub2ApiPasswordFailure }
  | { status: "protocol-required"; selection: Sub2ApiProtocolSelection; expiresAt: string }
  | { status: "local-2fa"; login: LoginData }
  | { status: "authenticated"; summary?: Sub2ApiImportSummary }
  | { status: "error"; error: Sub2ApiPasswordFailure; summary?: Sub2ApiImportSummary }
  | { status: "superseded" };

type AttemptStage = "password" | "remote_2fa" | "protocol_selection";

type PasswordAttempt = {
  id: number;
  generation: number;
  stage: AttemptStage;
  flowID: string | null;
  expiresAt: string;
  expiresAtMs: number;
  maskedEmail: string;
  selection: Sub2ApiProtocolSelection | null;
  decisions: Map<number, Sub2ApiProtocolDecision>;
  inFlight: Promise<Sub2ApiPasswordOutcome> | null;
  expiryTimer: ReturnType<typeof setTimeout> | null;
  cancellationRequested: boolean;
  finished: boolean;
};

type TrackedRequest = { id: number; promise: Promise<Sub2ApiPasswordOutcome> };

let nextID = 0;
let current: PasswordAttempt | null = null;
const expirationListeners = new Set<(id: number) => void>();

function isCurrent(attempt: PasswordAttempt): boolean {
  return current === attempt && !attempt.finished && authTransition.isCurrent(attempt.generation);
}

function queueCancellation(attempt: PasswordAttempt, flowID: string): void {
  if (attempt.cancellationRequested) return;
  attempt.cancellationRequested = true;
  void authTransition.run(async () => {
    try { await cancelSub2ApiPasswordLogin(flowID); } catch { /* best effort */ }
  });
}

function close(attempt: PasswordAttempt, reason: "succeeded" | "failed" | "cancelled" | "expired" | "superseded"): void {
  if (attempt.finished) return;
  const ownsAuthentication = current === attempt && authTransition.isCurrent(attempt.generation);
  if (reason !== "succeeded" && attempt.flowID) queueCancellation(attempt, attempt.flowID);
  attempt.finished = true;
  attempt.inFlight = null;
  attempt.flowID = null;
  attempt.selection = null;
  attempt.decisions.clear();
  attempt.maskedEmail = "";
  if (attempt.expiryTimer !== null) clearTimeout(attempt.expiryTimer);
  attempt.expiryTimer = null;
  // Invalidate immediately even though the queued HTTP operation is drained, so no stale completion can publish a session.
  if (ownsAuthentication && (reason === "cancelled" || reason === "expired" || reason === "superseded")) {
    authTransition.beginSwitch();
  }
  if (reason === "expired" && current === attempt) {
    for (const listener of expirationListeners) listener(attempt.id);
  }
}

function scheduleExpiry(attempt: PasswordAttempt): void {
  if (attempt.expiryTimer !== null) clearTimeout(attempt.expiryTimer);
  const remaining = Math.max(0, attempt.expiresAtMs - Date.now());
  attempt.expiryTimer = setTimeout(() => {
    if (current === attempt && !attempt.finished) {
      close(attempt, authTransition.isCurrent(attempt.generation) ? "expired" : "superseded");
    }
  }, remaining);
}

function track(attempt: PasswordAttempt, promise: Promise<Sub2ApiPasswordOutcome>): Promise<Sub2ApiPasswordOutcome> {
  attempt.inFlight = promise;
  void promise.then(() => {
    if (attempt.inFlight === promise) attempt.inFlight = null;
  });
  return promise;
}

function errorOutcome(attempt: PasswordAttempt, error: unknown): Sub2ApiPasswordOutcome {
  const summary = error instanceof ApiError ? readSub2ApiImportSummaryFromDetails(error.details) : null;
  const failure = classifySub2ApiPasswordFailure(error);
  close(attempt, "failed");
  return { status: "error", error: failure, ...(summary ? { summary } : {}) };
}

function invalidResponse(attempt: PasswordAttempt): Sub2ApiPasswordOutcome {
  close(attempt, "failed");
  return { status: "error", error: { legacy: false, reason: "unavailable" } };
}

function adoptFlow(attempt: PasswordAttempt, input: { flowID: string; expiresAt: string; expiresAtMs: number }): boolean {
  if (!isSub2ApiPasswordFlowExpiryValid(input.expiresAtMs)) return false;
  // The server creates its flow after request transit/settings lookup. Adopt
  // that first deadline without confusing it with the earlier client start.
  // Later responses must keep the same server deadline, and the local five-
  // minute ceiling is never extended.
  if (attempt.flowID && (attempt.flowID !== input.flowID || attempt.expiresAt !== input.expiresAt)) return false;
  attempt.flowID = input.flowID;
  attempt.expiresAt = input.expiresAt;
  attempt.expiresAtMs = Math.min(attempt.expiresAtMs, input.expiresAtMs);
  scheduleExpiry(attempt);
  return true;
}

async function discardResponse(attempt: PasswordAttempt, value: unknown): Promise<Sub2ApiPasswordOutcome> {
  const flowID = readSub2ApiPasswordFlowID(value);
  if (flowID && !attempt.cancellationRequested) {
    attempt.cancellationRequested = true;
    try { await cancelSub2ApiPasswordLogin(flowID); } catch { /* best effort inside the drained queue */ }
  }
  return { status: "superseded" };
}

async function applyProgress(attempt: PasswordAttempt, progress: NonNullable<ReturnType<typeof parseSub2ApiPasswordProgress>>): Promise<Sub2ApiPasswordOutcome> {
  if (progress.stage === "remote_2fa") {
    if (attempt.stage !== "password" || !adoptFlow(attempt, progress)) return invalidResponse(attempt);
    attempt.stage = "remote_2fa";
    attempt.maskedEmail = progress.maskedEmail;
    return { status: "remote-2fa", maskedEmail: progress.maskedEmail, expiresAt: progress.expiresAt };
  }
  if (progress.stage === "local_2fa") {
    const login = progress.login;
    if (!writeTwoFactorChallenge(login.twoFactorChallengeToken ?? "", login.verificationMethods)) {
      close(attempt, "failed");
      return { status: "error", error: { legacy: true, reason: "two-factor-failed" } };
    }
    rememberPendingSub2ApiSummary(login.twoFactorChallengeToken ?? "", parseSub2ApiImportSummary(login.sub2apiImport));
    close(attempt, "succeeded");
    return { status: "local-2fa", login };
  }
  try {
    await completeNativeSignIn(progress.login, attempt.generation);
  } catch (error) {
    return errorOutcome(attempt, error);
  }
  if (!isCurrent(attempt)) return { status: "superseded" };
  const summary = parseSub2ApiImportSummary(progress.login.sub2apiImport);
  close(attempt, "succeeded");
  return { status: "authenticated", ...(summary ? { summary } : {}) };
}

async function execute(
  attempt: PasswordAttempt,
  request: () => Promise<unknown>,
  submittedDecisions?: ReadonlyMap<number, Sub2ApiProtocolDecision>,
): Promise<Sub2ApiPasswordOutcome> {
  if (!isCurrent(attempt)) return { status: "superseded" };
  try {
    const value = await request();
    if (!isCurrent(attempt)) return discardResponse(attempt, value);
    const progress = parseSub2ApiPasswordProgress(value);
    return progress ? applyProgress(attempt, progress) : invalidResponse(attempt);
  } catch (error) {
    if (!isCurrent(attempt)) {
      const details = error instanceof ApiError ? { details: error.details } : null;
      return discardResponse(attempt, details);
    }
    if (error instanceof ApiError) {
      const protocol = parseSub2ApiPasswordProtocolRequired(error);
      if (protocol) {
        if ((attempt.flowID && attempt.flowID !== protocol.flowID) || !adoptFlow(attempt, protocol)) return invalidResponse(attempt);
        attempt.stage = "protocol_selection";
        attempt.selection = protocol.selection;
        if (submittedDecisions) attempt.decisions = new Map(submittedDecisions);
        return { status: "protocol-required", selection: protocol.selection, expiresAt: protocol.expiresAt };
      }
      const retry = parseSub2ApiRemoteTwoFactorRetry(error);
      if (retry && attempt.stage === "remote_2fa" && retry.flowID === attempt.flowID && retry.expiresAt === attempt.expiresAt && isSub2ApiPasswordFlowExpiryValid(retry.expiresAtMs)) {
        return { status: "remote-2fa", maskedEmail: attempt.maskedEmail, expiresAt: attempt.expiresAt, error: classifySub2ApiPasswordFailure(error) };
      }
    }
    return errorOutcome(attempt, error);
  }
}

export function startSub2ApiPasswordLogin(email: string, password: string, captcha: Sub2ApiPasswordCaptcha): TrackedRequest {
  if (current?.inFlight && !current.finished) return { id: current.id, promise: current.inFlight };
  if (current && !current.finished) close(current, "superseded");
  const attempt: PasswordAttempt = {
    id: ++nextID,
    generation: authTransition.beginSwitch(),
    stage: "password",
    flowID: null,
    expiresAt: "",
    expiresAtMs: Date.now() + SUB2API_PASSWORD_FLOW_TTL_MS,
    maskedEmail: "",
    selection: null,
    decisions: new Map(),
    inFlight: null,
    expiryTimer: null,
    cancellationRequested: false,
    finished: false,
  };
  current = attempt;
  scheduleExpiry(attempt);
  const promise = authTransition.run(() => execute(attempt, () => loginSub2ApiPassword(email.trim(), password, captcha)));
  return { id: attempt.id, promise: track(attempt, promise) };
}

export function submitSub2ApiRemoteTwoFactor(id: number, code: string): Promise<Sub2ApiPasswordOutcome> {
  const attempt = current;
  if (!attempt || attempt.id !== id || !isCurrent(attempt)) return Promise.resolve({ status: "superseded" });
  if (attempt.inFlight) return attempt.inFlight;
  if (attempt.stage !== "remote_2fa" || !attempt.flowID) return Promise.resolve({ status: "superseded" });
  const flowID = attempt.flowID;
  return track(attempt, authTransition.run(() => execute(attempt, () => verifySub2ApiPasswordTwoFactor(flowID, code))));
}

export function submitSub2ApiPasswordProtocols(id: number, decisions: ReadonlyMap<number, Sub2ApiProtocolDecision>): Promise<Sub2ApiPasswordOutcome> {
  const attempt = current;
  if (!attempt || attempt.id !== id || !isCurrent(attempt)) return Promise.resolve({ status: "superseded" });
  if (attempt.inFlight) return attempt.inFlight;
  const selection = attempt.selection;
  if (attempt.stage !== "protocol_selection" || !attempt.flowID || !selection) return Promise.resolve({ status: "superseded" });
  const currentOptions = createSub2ApiProtocolExchangeOptions(selection, decisions);
  if (!currentOptions) return Promise.resolve({ status: "protocol-required", selection, expiresAt: attempt.expiresAt });
  const options = mergeSub2ApiProtocolExchangeOptions(attempt.decisions, currentOptions, selection.protocols);
  if (!options) return Promise.resolve(invalidResponse(attempt));
  const submittedDecisions = new Map(attempt.decisions);
  for (const [keyID, decision] of decisions) submittedDecisions.set(keyID, decision);
  const flowID = attempt.flowID;
  return track(attempt, authTransition.run(() => execute(attempt, () => continueSub2ApiPasswordLogin(flowID, options), submittedDecisions)));
}

export function cancelSub2ApiPasswordAttempt(id: number): void {
  if (current?.id === id && !current.finished) close(current, "cancelled");
}

export function subscribeSub2ApiPasswordExpiration(listener: (id: number) => void): () => void {
  expirationListeners.add(listener);
  return () => { expirationListeners.delete(listener); };
}

subscribeSub2ApiBootstrap(() => {
  if (current && !current.finished && !authTransition.isCurrent(current.generation)) {
    close(current, "superseded");
  }
});

if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => {
    if (current && !current.finished) close(current, "cancelled");
  });
}
