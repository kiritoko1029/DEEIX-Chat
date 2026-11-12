import * as React from "react";
import { usePathname, useRouter } from "next/navigation";

import {
  cancelSub2ApiBootstrap,
  runSub2ApiBootstrap,
  submitSub2ApiProtocolDecisions,
  type Sub2ApiBootstrapFailure,
  type Sub2ApiBootstrapOutcome,
} from "@/features/auth/model/sub2api-bootstrap";
import { readSub2ApiAttempt, subscribeSub2ApiBootstrap } from "@/shared/auth/sub2api-bootstrap";
import { useAuthSub2ApiImportSummary } from "@/features/auth/hooks/use-auth-sub2api-import-summary";
import type { Sub2ApiImportSummary, Sub2ApiProtocolSelection } from "@/shared/api/auth-types";
import type { Sub2ApiProtocolDecision } from "@/shared/auth/sub2api-protocol";

type Sub2ApiBootstrapState =
  | { status: "checking" | "loading" | "handoff" | "ready" }
  | { status: "protocol-required"; selection: Sub2ApiProtocolSelection; expiresAt: number }
  | { status: "error"; reason: Sub2ApiBootstrapFailure; summary?: Sub2ApiImportSummary };

export function useAuthSub2ApiBootstrap() {
  const router = useRouter();
  const pathname = usePathname();
  const showImportSummary = useAuthSub2ApiImportSummary();
  const showImportSummaryRef = React.useRef(showImportSummary);
  showImportSummaryRef.current = showImportSummary;
  const attempt = React.useSyncExternalStore(subscribeSub2ApiBootstrap, readSub2ApiAttempt, () => 0);
  const [resolved, setResolved] = React.useState<{ attempt: number; state: Sub2ApiBootstrapState }>({
    attempt: 0, state: { status: "checking" },
  });
  const continuationRef = React.useRef<{ attempt: number; promise: Promise<Sub2ApiBootstrapOutcome> } | null>(null);
  // A new entry hides the previous account before its effect starts.
  const state: Sub2ApiBootstrapState = resolved.attempt === attempt ? resolved.state : { status: "loading" };

  const applyOutcome = React.useCallback((targetAttempt: number, outcome: Sub2ApiBootstrapOutcome) => {
    if (targetAttempt !== readSub2ApiAttempt()) return;
    if (outcome.status === "superseded") return;
    if (outcome.status === "two-factor") {
      setResolved({ attempt: targetAttempt, state: { status: "handoff" } });
      router.replace("/login");
    } else if (outcome.status === "signed-in") {
      showImportSummaryRef.current(outcome.summary);
      setResolved({ attempt: targetAttempt, state: { status: "ready" } });
    } else {
      setResolved({ attempt: targetAttempt, state: outcome });
    }
  }, [router]);

  React.useEffect(() => {
    if (!attempt) {
      setResolved({ attempt, state: { status: "ready" } });
      return;
    }
    let active = true;
    void runSub2ApiBootstrap(attempt).then((outcome) => {
      if (active) applyOutcome(attempt, outcome);
    });
    return () => { active = false; };
  }, [attempt, applyOutcome]);

  React.useEffect(() => {
    if (pathname === "/login" && state.status === "handoff") {
      setResolved({ attempt, state: { status: "ready" } });
    }
  }, [attempt, pathname, state.status]);

  React.useEffect(() => {
    // Capture runs before Next's pathname subscription catches up on History
    // navigation. Use the actual URL so a new root entry is not cancelled by
    // the previous render's /login pathname.
    if (window.location.pathname !== "/" && (state.status === "loading" || state.status === "protocol-required")) {
      cancelSub2ApiBootstrap(attempt);
      continuationRef.current = null;
      setResolved({ attempt, state: { status: "handoff" } });
    }
  }, [attempt, pathname, state.status]);

  React.useEffect(() => {
    if (state.status !== "protocol-required") return undefined;
    const expire = () => {
      cancelSub2ApiBootstrap(attempt);
      continuationRef.current = null;
      setResolved({ attempt, state: { status: "error", reason: "protocol-expired" } });
    };
    const remaining = state.expiresAt - Date.now();
    if (remaining <= 0) {
      expire();
      return undefined;
    }
    const timer = window.setTimeout(expire, remaining);
    return () => window.clearTimeout(timer);
  }, [attempt, state]);

  const submitProtocolDecisions = React.useCallback((decisions: ReadonlyMap<number, Sub2ApiProtocolDecision>) => {
    if (continuationRef.current?.attempt === attempt) return;
    setResolved({ attempt, state: { status: "loading" } });
    const promise = submitSub2ApiProtocolDecisions(attempt, decisions);
    continuationRef.current = { attempt, promise };
    void promise.then((outcome) => {
      if (continuationRef.current?.promise === promise) continuationRef.current = null;
      applyOutcome(attempt, outcome);
    });
  }, [attempt, applyOutcome]);

  const returnToLogin = React.useCallback(() => {
    cancelSub2ApiBootstrap(attempt);
    continuationRef.current = null;
    setResolved({ attempt, state: { status: "handoff" } });
    router.replace("/login");
  }, [attempt, router]);

  return { state, submitProtocolDecisions, returnToLogin };
}
