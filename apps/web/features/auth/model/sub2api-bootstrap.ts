import { exchangeSub2Api } from "@/shared/api/auth";
import type { Sub2ApiImportSummary } from "@/shared/api/auth-types";
import { ApiError } from "@/shared/api/http-client";
import { classifySub2ApiFailure, type Sub2ApiBootstrapFailure } from "@/shared/auth/sub2api-failure";
import { clearTwoFactorChallenge, writeTwoFactorChallenge } from "@/features/auth/model/login-page";
import { createSub2ApiBootstrapCoordinator } from "@/shared/auth/sub2api-coordinator";
import { authTransition } from "@/shared/auth/auth-transition";
import { consumeSub2ApiBootstrap, readSub2ApiAttempt } from "@/shared/auth/sub2api-bootstrap";
import { clearNativeSessionForAccountSwitch, completeNativeSignIn } from "@/shared/platform/desktop-session";
import { parseSub2ApiImportSummary, readSub2ApiImportSummaryFromDetails } from "@/features/auth/model/sub2api-import-summary";
import { clearPendingSub2ApiSummary, rememberPendingSub2ApiSummary } from "@/features/auth/model/sub2api-pending-summary";

export type { Sub2ApiBootstrapFailure } from "@/shared/auth/sub2api-failure";
export type Sub2ApiBootstrapOutcome =
  | { status: "signed-in"; summary?: Sub2ApiImportSummary }
  | { status: "two-factor" | "superseded" }
  | { status: "error"; reason: Sub2ApiBootstrapFailure; summary?: Sub2ApiImportSummary };

let current: { id: number; promise: Promise<Sub2ApiBootstrapOutcome> } | null = null;

// A promise belongs to an entry attempt, not to the entire document lifetime.
// Queue the whole operation (including native writes), not just fetch: even
// Set-Cookie from a slow old response must finish before the new exchange.
export function runSub2ApiBootstrap(id: number): Promise<Sub2ApiBootstrapOutcome> {
  if (!authTransition.isCurrent(id) || readSub2ApiAttempt() !== id) return Promise.resolve({ status: "superseded" });
  if (current?.id === id) return current.promise;
  const captured = consumeSub2ApiBootstrap();
  const isCurrent = () => authTransition.isCurrent(id) && readSub2ApiAttempt() === id;
  clearTwoFactorChallenge();
  clearPendingSub2ApiSummary();
  const coordinator = createSub2ApiBootstrapCoordinator(() => authTransition.run<Sub2ApiBootstrapOutcome>(async () => {
    if (!isCurrent()) return { status: "superseded" };
    try {
      // Invalid links isolate and drop native credentials too.
      await clearNativeSessionForAccountSwitch();
      if (!isCurrent()) return { status: "superseded" };
      if (!captured || captured.kind === "error") return { status: "error", reason: "invalid-link" };
      const result = await exchangeSub2Api(captured.credentials.userID, captured.credentials.token);
      if (!isCurrent()) return { status: "superseded" };
      if (result.twoFactorRequired) {
        if (!writeTwoFactorChallenge(result.twoFactorChallengeToken ?? "", result.verificationMethods)) {
          return { status: "error", reason: "two-factor-failed" };
        }
        rememberPendingSub2ApiSummary(result.twoFactorChallengeToken ?? "", parseSub2ApiImportSummary(result.sub2apiImport));
        return { status: "two-factor" };
      }
      await completeNativeSignIn(result, id);
      const summary = parseSub2ApiImportSummary(result.sub2apiImport);
      return { status: "signed-in", ...(summary ? { summary } : {}) };
    } catch (error) {
      if (!isCurrent()) return { status: "superseded" };
      clearTwoFactorChallenge();
      const reason = error instanceof ApiError
        ? classifySub2ApiFailure(error.errorCode, error.details, error.status)
        : "exchange-failed";
      const summary = error instanceof ApiError ? readSub2ApiImportSummaryFromDetails(error.details) : null;
      return { status: "error", reason, ...(summary ? { summary } : {}) };
    }
  }));
  const promise = coordinator.run();
  current = { id, promise };
  return promise;
}
