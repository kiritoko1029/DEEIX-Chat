"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

import { getSub2ApiPasswordLoginOptions } from "@/shared/api/auth";
import type { LoginData, Sub2ApiImportSummary, Sub2ApiPasswordCaptcha, Sub2ApiPasswordLoginOptions, Sub2ApiProtocolSelection } from "@/shared/api/auth-types";
import { isDesktopApp } from "@/shared/platform";
import type { Sub2ApiProtocolDecision } from "@/shared/auth/sub2api-protocol";
import { useAuthSub2ApiImportSummary } from "@/features/auth/hooks/use-auth-sub2api-import-summary";
import {
  cancelSub2ApiPasswordAttempt,
  startSub2ApiPasswordLogin,
  submitSub2ApiPasswordProtocols,
  submitSub2ApiRemoteTwoFactor,
  subscribeSub2ApiPasswordExpiration,
  type Sub2ApiPasswordOutcome,
} from "@/features/auth/model/sub2api-password-controller";
import { parseSub2ApiPasswordLoginOptions, type Sub2ApiPasswordFailure } from "@/features/auth/model/sub2api-password-login";

export type Sub2ApiPasswordLoginStage =
  | { stage: "form" }
  | { stage: "remote-2fa"; maskedEmail: string; expiresAt: string }
  | { stage: "protocol-selection"; selection: Sub2ApiProtocolSelection; expiresAt: string }
  | { stage: "done" };

type Input = {
  nextPath: string;
  localChallengeActive: boolean;
  onLocalTwoFactor: (login: LoginData) => void;
};

export function useAuthSub2ApiPasswordLogin({ nextPath, localChallengeActive, onLocalTwoFactor }: Input) {
  const router = useRouter();
  const showImportSummary = useAuthSub2ApiImportSummary();
  const onLocalTwoFactorRef = React.useRef(onLocalTwoFactor);
  const showImportSummaryRef = React.useRef(showImportSummary);
  onLocalTwoFactorRef.current = onLocalTwoFactor;
  showImportSummaryRef.current = showImportSummary;
  const [optionsReady, setOptionsReady] = React.useState(isDesktopApp());
  const [options, setOptions] = React.useState<Sub2ApiPasswordLoginOptions | null>(null);
  const [mode, setMode] = React.useState<"local" | "sub2api">("local");
  const [stage, setStage] = React.useState<Sub2ApiPasswordLoginStage>({ stage: "form" });
  const [error, setError] = React.useState<Sub2ApiPasswordFailure | null>(null);
  const [errorSummary, setErrorSummary] = React.useState<Sub2ApiImportSummary | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const attemptRef = React.useRef<number | null>(null);
  const submittingRef = React.useRef(false);
  const explicitLocalRef = React.useRef(false);
  const available = !isDesktopApp() && options?.enabled === true && options.passwordLoginAvailable && options.browserOnly;

  React.useEffect(() => {
    if (isDesktopApp()) return undefined;
    let active = true;
    const timer = window.setTimeout(() => {
      if (active) setOptionsReady(true);
    }, 10_000);
    void getSub2ApiPasswordLoginOptions()
      .then((value) => { if (active) setOptions(parseSub2ApiPasswordLoginOptions(value)); })
      .catch(() => { if (active) setOptions(null); })
      .finally(() => { if (active) setOptionsReady(true); });
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, []);

  React.useEffect(() => {
    if (available && !localChallengeActive && !explicitLocalRef.current) setMode("sub2api");
  }, [available, localChallengeActive]);

  const clearAttempt = React.useCallback(() => {
    const id = attemptRef.current;
    attemptRef.current = null;
    submittingRef.current = false;
    if (id !== null) cancelSub2ApiPasswordAttempt(id);
    setSubmitting(false);
    setStage({ stage: "form" });
    setError(null);
    setErrorSummary(null);
  }, []);

  React.useEffect(() => {
    if (!localChallengeActive) return;
    explicitLocalRef.current = true;
    clearAttempt();
    setMode("local");
  }, [clearAttempt, localChallengeActive]);

  React.useEffect(() => subscribeSub2ApiPasswordExpiration((id) => {
    if (attemptRef.current !== id) return;
    attemptRef.current = null;
    submittingRef.current = false;
    setSubmitting(false);
    setStage({ stage: "form" });
    setError({ legacy: false, reason: "flow-expired" });
    setErrorSummary(null);
  }), []);

  const applyOutcome = React.useCallback((id: number, outcome: Sub2ApiPasswordOutcome) => {
    if (attemptRef.current !== id) return;
    if (outcome.status === "superseded") return;
    if (outcome.status === "remote-2fa") {
      setStage({ stage: "remote-2fa", maskedEmail: outcome.maskedEmail, expiresAt: outcome.expiresAt });
      setError(outcome.error ?? null);
      setErrorSummary(null);
    } else if (outcome.status === "protocol-required") {
      setStage({ stage: "protocol-selection", selection: outcome.selection, expiresAt: outcome.expiresAt });
      setError(null);
      setErrorSummary(null);
    } else if (outcome.status === "error") {
      attemptRef.current = null;
      setStage({ stage: "form" });
      setError(outcome.error);
      setErrorSummary(outcome.summary ?? null);
    } else {
      attemptRef.current = null;
      setStage({ stage: "done" });
      setError(null);
      setErrorSummary(null);
      if (outcome.status === "local-2fa") {
        onLocalTwoFactorRef.current(outcome.login);
      } else {
        showImportSummaryRef.current(outcome.summary);
        router.replace(nextPath);
      }
    }
    submittingRef.current = false;
    setSubmitting(false);
  }, [nextPath, router]);

  const watch = React.useCallback((id: number, promise: Promise<Sub2ApiPasswordOutcome>) => {
    attemptRef.current = id;
    submittingRef.current = true;
    setSubmitting(true);
    void promise.then((outcome) => applyOutcome(id, outcome));
  }, [applyOutcome]);

  const submitPassword = React.useCallback(async (email: string, password: string, captcha: Sub2ApiPasswordCaptcha) => {
    if (!available || submittingRef.current) return;
    setError(null);
    setErrorSummary(null);
    const request = startSub2ApiPasswordLogin(email, password, captcha);
    watch(request.id, request.promise);
  }, [available, watch]);

  const submitRemoteCode = React.useCallback(async (code: string) => {
    const id = attemptRef.current;
    if (id === null || submittingRef.current) return;
    watch(id, submitSub2ApiRemoteTwoFactor(id, code));
  }, [watch]);

  const submitProtocols = React.useCallback(async (decisions: ReadonlyMap<number, Sub2ApiProtocolDecision>) => {
    const id = attemptRef.current;
    if (id === null || submittingRef.current) return;
    watch(id, submitSub2ApiPasswordProtocols(id, decisions));
  }, [watch]);

  const switchMode = React.useCallback((next: "local" | "sub2api") => {
    clearAttempt();
    explicitLocalRef.current = next === "local";
    setMode(next);
  }, [clearAttempt]);

  return {
    available,
    options,
    optionsReady,
    mode,
    stage,
    submitting,
    error,
    errorSummary,
    submitPassword,
    submitRemoteCode,
    submitProtocols,
    cancel: clearAttempt,
    switchMode,
  };
}
