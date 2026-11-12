import { exchangeSub2Api } from "@/shared/api/auth";
import type { Sub2ApiExchangeOptions, Sub2ApiImportSummary, Sub2ApiProtocolSelection } from "@/shared/api/auth-types";
import { ApiError } from "@/shared/api/http-client";
import { classifySub2ApiFailure, type Sub2ApiBootstrapFailure } from "@/shared/auth/sub2api-failure";
import { clearTwoFactorChallenge, writeTwoFactorChallenge } from "@/features/auth/model/login-page";
import { createSub2ApiBootstrapCoordinator } from "@/shared/auth/sub2api-coordinator";
import { authTransition } from "@/shared/auth/auth-transition";
import { consumeSub2ApiBootstrap, readSub2ApiAttempt, subscribeSub2ApiBootstrap } from "@/shared/auth/sub2api-bootstrap";
import { clearNativeSessionForAccountSwitch, completeNativeSignIn } from "@/shared/platform/desktop-session";
import { parseSub2ApiImportSummary, readSub2ApiImportSummaryFromDetails } from "@/features/auth/model/sub2api-import-summary";
import { clearPendingSub2ApiSummary, rememberPendingSub2ApiSummary } from "@/features/auth/model/sub2api-pending-summary";
import { createSub2ApiAttempt, SUB2API_ATTEMPT_TTL_MS, type Sub2ApiAttempt, type Sub2ApiAttemptCloseReason } from "@/shared/auth/sub2api-attempt";
import { createSub2ApiProtocolExchangeOptions, mergeSub2ApiProtocolExchangeOptions, parseSub2ApiProtocolSelection, type Sub2ApiProtocolDecision } from "@/shared/auth/sub2api-protocol";

export type { Sub2ApiBootstrapFailure } from "@/shared/auth/sub2api-failure";
export type Sub2ApiBootstrapOutcome =
  | { status: "signed-in"; summary?: Sub2ApiImportSummary }
  | { status: "two-factor" }
  | { status: "superseded" }
  | { status: "protocol-required"; selection: Sub2ApiProtocolSelection; expiresAt: number }
  | { status: "error"; reason: Sub2ApiBootstrapFailure; summary?: Sub2ApiImportSummary };

type CurrentSub2ApiBootstrap = {
  id: number;
  promise: Promise<Sub2ApiBootstrapOutcome> | null;
  attempt: Sub2ApiAttempt | null;
  abortController: AbortController;
  decisions: Map<number, Sub2ApiProtocolDecision>;
  continuation: Promise<Sub2ApiBootstrapOutcome> | null;
  expiryTimer: ReturnType<typeof setTimeout> | null;
  finished: boolean;
  closedGeneration: number | null;
};

let current: CurrentSub2ApiBootstrap | null = null;
let leaveListenerInstalled = false;

function closeCurrent(entry: CurrentSub2ApiBootstrap, reason: Sub2ApiAttemptCloseReason): void {
  if (entry.finished) return;
  const ownsAuthentication = current === entry
    && authTransition.isCurrent(entry.id)
    && readSub2ApiAttempt() === entry.id;
  entry.attempt?.close(reason);
  entry.abortController.abort();
  entry.decisions.clear();
  entry.finished = true;
  entry.continuation = null;
  if (entry.expiryTimer !== null) clearTimeout(entry.expiryTimer);
  entry.expiryTimer = null;
  // Native keychain writes cannot be aborted. Invalidate their generation so
  // completeNativeSignIn drains and clears them instead of publishing a cancelled session.
  if (ownsAuthentication && (reason === "cancelled" || reason === "expired")) {
    entry.closedGeneration = authTransition.beginSwitch();
  }
}

function isCurrent(entry: CurrentSub2ApiBootstrap): boolean {
  return current === entry
    && !entry.finished
    && authTransition.isCurrent(entry.id)
    && readSub2ApiAttempt() === entry.id
    && !entry.attempt?.closeReason;
}

function supersededOutcome(entry: CurrentSub2ApiBootstrap): Sub2ApiBootstrapOutcome {
  if (current === entry
    && entry.attempt?.closeReason === "expired"
    && (authTransition.isCurrent(entry.id) || (entry.closedGeneration !== null && authTransition.isCurrent(entry.closedGeneration)))
    && readSub2ApiAttempt() === entry.id) {
    closeCurrent(entry, "expired");
    return { status: "error", reason: "protocol-expired" };
  }
  return { status: "superseded" };
}

function protocolRequired(error: ApiError): Sub2ApiProtocolSelection | null {
  return parseSub2ApiProtocolSelection(error.status, error.errorCode, error.details);
}

async function exchangeAndComplete(
  entry: CurrentSub2ApiBootstrap,
  options?: Sub2ApiExchangeOptions,
  submittedDecisions?: ReadonlyMap<number, Sub2ApiProtocolDecision>,
): Promise<Sub2ApiBootstrapOutcome> {
  if (!isCurrent(entry)) return supersededOutcome(entry);
  try {
    const credentials = entry.attempt?.readCredentials();
    if (!credentials) return supersededOutcome(entry);
    const result = await exchangeSub2Api(credentials.userID, credentials.token, options, entry.abortController.signal);
    if (!isCurrent(entry)) return supersededOutcome(entry);
    if (result.twoFactorRequired) {
      if (!writeTwoFactorChallenge(result.twoFactorChallengeToken ?? "", result.verificationMethods)) {
        closeCurrent(entry, "failed");
        return { status: "error", reason: "two-factor-failed" };
      }
      rememberPendingSub2ApiSummary(result.twoFactorChallengeToken ?? "", parseSub2ApiImportSummary(result.sub2apiImport));
      closeCurrent(entry, "succeeded");
      return { status: "two-factor" };
    }
    await completeNativeSignIn(result, entry.id);
    if (!authTransition.isCurrent(entry.id) || readSub2ApiAttempt() !== entry.id || current !== entry) {
      return { status: "superseded" };
    }
    const summary = parseSub2ApiImportSummary(result.sub2apiImport);
    closeCurrent(entry, "succeeded");
    return { status: "signed-in", ...(summary ? { summary } : {}) };
  } catch (error) {
    if (!isCurrent(entry)) return supersededOutcome(entry);
    clearTwoFactorChallenge();
    if (error instanceof ApiError) {
      const selection = protocolRequired(error);
      if (selection) {
        if (entry.attempt?.requireProtocol(selection)) {
          if (submittedDecisions) entry.decisions = new Map(submittedDecisions);
          return { status: "protocol-required", selection, expiresAt: entry.attempt.expiresAt };
        }
        return supersededOutcome(entry);
      }
    }
    const reason = error instanceof ApiError
      ? classifySub2ApiFailure(error.errorCode, error.details, error.status)
      : "exchange-failed";
    const summary = error instanceof ApiError ? readSub2ApiImportSummaryFromDetails(error.details) : null;
    closeCurrent(entry, "failed");
    return { status: "error", reason, ...(summary ? { summary } : {}) };
  }
}

// A promise belongs to an entry attempt, not to the entire document lifetime.
// Queue HTTP and native writes together. Superseding aborts the old fetch;
// native writes still drain through the queue before the new attempt commits.
export function runSub2ApiBootstrap(id: number): Promise<Sub2ApiBootstrapOutcome> {
  if (!authTransition.isCurrent(id) || readSub2ApiAttempt() !== id) return Promise.resolve({ status: "superseded" });
  if (current?.id === id) return current.promise ?? Promise.resolve({ status: "superseded" });
  if (current) closeCurrent(current, "superseded");

  const captured = consumeSub2ApiBootstrap();
  const invalidLink = !captured || captured.kind === "error";
  const attempt = captured?.kind === "exchange" ? createSub2ApiAttempt(id, captured.credentials) : null;
  const entry: CurrentSub2ApiBootstrap = {
    id,
    promise: null,
    attempt,
    abortController: new AbortController(),
    decisions: new Map(),
    continuation: null,
    expiryTimer: null,
    finished: false,
    closedGeneration: null,
  };
  current = entry;
  if (attempt) {
    entry.expiryTimer = setTimeout(() => {
      if (current === entry && !entry.finished) closeCurrent(entry, "expired");
    }, SUB2API_ATTEMPT_TTL_MS);
  }
  if (!leaveListenerInstalled && typeof window !== "undefined") {
    leaveListenerInstalled = true;
    window.addEventListener("pagehide", () => {
      if (current) closeCurrent(current, "cancelled");
    });
  }

  clearTwoFactorChallenge();
  clearPendingSub2ApiSummary();
  const coordinator = createSub2ApiBootstrapCoordinator(() => authTransition.run<Sub2ApiBootstrapOutcome>(async () => {
    if (!isCurrent(entry)) return supersededOutcome(entry);
    try {
      await clearNativeSessionForAccountSwitch();
    } catch {
      if (!isCurrent(entry)) return supersededOutcome(entry);
      clearTwoFactorChallenge();
      closeCurrent(entry, "failed");
      return { status: "error", reason: "exchange-failed" };
    }
    if (!isCurrent(entry)) return supersededOutcome(entry);
    if (invalidLink) {
      closeCurrent(entry, "failed");
      return { status: "error", reason: "invalid-link" };
    }
    return exchangeAndComplete(entry);
  }));
  entry.promise = coordinator.run();
  return entry.promise;
}

export function submitSub2ApiProtocolDecisions(
  id: number,
  decisions: ReadonlyMap<number, Sub2ApiProtocolDecision>,
): Promise<Sub2ApiBootstrapOutcome> {
  const entry = current;
  if (!entry || entry.id !== id || !authTransition.isCurrent(id) || readSub2ApiAttempt() !== id || entry.finished) {
    return Promise.resolve({ status: "superseded" });
  }
  if (entry.continuation) return entry.continuation;
  const selection = entry.attempt?.readSelection();
  if (!selection || !entry.attempt) return Promise.resolve(supersededOutcome(entry));
  const currentOptions = createSub2ApiProtocolExchangeOptions(selection, decisions);
  if (!currentOptions) return Promise.resolve({ status: "protocol-required", selection, expiresAt: entry.attempt.expiresAt });
  const options = mergeSub2ApiProtocolExchangeOptions(entry.decisions, currentOptions, selection.protocols);
  if (!options) {
    closeCurrent(entry, "failed");
    return Promise.resolve({ status: "error", reason: "exchange-failed" });
  }
  const submittedDecisions = new Map(entry.decisions);
  for (const [keyID, decision] of decisions) submittedDecisions.set(keyID, decision);

  const promise = authTransition.run<Sub2ApiBootstrapOutcome>(() => exchangeAndComplete(entry, options, submittedDecisions));
  entry.continuation = promise;
  void promise.then(() => {
    if (entry.continuation === promise) entry.continuation = null;
  });
  return promise;
}

export function cancelSub2ApiBootstrap(id: number): void {
  const entry = current;
  if (!entry || entry.id !== id || entry.finished) return;
  const ownsAuthentication = authTransition.isCurrent(id) && readSub2ApiAttempt() === id;
  closeCurrent(entry, "cancelled");
  if (ownsAuthentication) {
    clearTwoFactorChallenge();
    clearPendingSub2ApiSummary();
  }
}

subscribeSub2ApiBootstrap(() => {
  if (current && current.id !== readSub2ApiAttempt()) closeCurrent(current, "superseded");
});
