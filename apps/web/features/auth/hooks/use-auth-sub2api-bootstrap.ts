import * as React from "react";
import { usePathname, useRouter } from "next/navigation";

import { runSub2ApiBootstrap, type Sub2ApiBootstrapFailure } from "@/features/auth/model/sub2api-bootstrap";
import { readSub2ApiAttempt, subscribeSub2ApiBootstrap } from "@/shared/auth/sub2api-bootstrap";
import { useAuthSub2ApiImportSummary } from "@/features/auth/hooks/use-auth-sub2api-import-summary";

type Sub2ApiBootstrapState =
  | { status: "checking" | "loading" | "handoff" | "ready" }
  | { status: "error"; reason: Sub2ApiBootstrapFailure; summary?: import("@/shared/api/auth-types").Sub2ApiImportSummary };

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
  // A new entry hides the previous account before its effect starts.
  const state: Sub2ApiBootstrapState = resolved.attempt === attempt ? resolved.state : { status: "loading" };

  React.useEffect(() => {
    if (!attempt) {
      setResolved({ attempt, state: { status: "ready" } });
      return;
    }
    let active = true;
    void runSub2ApiBootstrap(attempt).then((outcome) => {
      if (!active || attempt !== readSub2ApiAttempt()) return;
      if (outcome.status === "two-factor") {
        setResolved({ attempt, state: { status: "handoff" } });
        router.replace("/login");
      } else if (outcome.status === "error") {
        setResolved({ attempt, state: outcome });
      } else {
        if (outcome.status === "signed-in") showImportSummaryRef.current(outcome.summary);
        setResolved({ attempt, state: { status: "ready" } });
      }
    });
    return () => { active = false; };
  }, [attempt, router]);

  React.useEffect(() => {
    if (pathname === "/login" && state.status === "handoff") {
      setResolved({ attempt, state: { status: "ready" } });
    }
  }, [attempt, pathname, state.status]);

  const returnToLogin = React.useCallback(() => {
    setResolved({ attempt, state: { status: "handoff" } });
    router.replace("/login");
  }, [attempt, router]);

  return { state, returnToLogin };
}
