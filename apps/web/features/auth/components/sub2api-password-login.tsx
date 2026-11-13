"use client";

import * as React from "react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SpinnerLabel } from "@/components/ui/spinner";
import { Sub2ApiImportSummaryDetails } from "@/features/auth/components/sub2api-import-summary-details";
import { Sub2ApiProtocolSelection } from "@/features/auth/components/sub2api-protocol-selection";
import { TurnstileWidget, type TurnstileWidgetStatus } from "@/features/auth/components/shared/turnstile-widget";
import type { Sub2ApiImportSummary, Sub2ApiPasswordCaptcha, Sub2ApiPasswordLoginOptions } from "@/shared/api/auth-types";
import { sub2ApiFailureMessageKeys } from "@/shared/auth/sub2api-failure";
import type { Sub2ApiProtocolDecision } from "@/shared/auth/sub2api-protocol";
import { openExternal } from "@/shared/platform/desktop-open";
import type { Sub2ApiPasswordLoginStage } from "@/features/auth/hooks/use-auth-sub2api-password-login";
import type { Sub2ApiPasswordFailure } from "@/features/auth/model/sub2api-password-login";
import { createSub2ApiImportSummaryView } from "@/features/auth/model/sub2api-import-summary";

type Props = {
  options: Sub2ApiPasswordLoginOptions;
  stage: Sub2ApiPasswordLoginStage;
  submitting: boolean;
  error: Sub2ApiPasswordFailure | null;
  errorSummary: Sub2ApiImportSummary | null;
  onPasswordSubmit: (email: string, password: string, captcha: Sub2ApiPasswordCaptcha) => Promise<void>;
  onRemoteCodeSubmit: (code: string) => Promise<void>;
  onProtocolSubmit: (decisions: ReadonlyMap<number, Sub2ApiProtocolDecision>) => Promise<void>;
  onCancel: () => void;
};

export function Sub2ApiPasswordLogin({ options, stage, submitting, error, errorSummary, onPasswordSubmit, onRemoteCodeSubmit, onProtocolSubmit, onCancel }: Props) {
  const t = useTranslations("login.sub2api.passwordLogin");
  const legacyT = useTranslations("login.sub2api");
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [code, setCode] = React.useState("");
  const [captchaToken, setCaptchaToken] = React.useState("");
  const [captchaResetSignal, setCaptchaResetSignal] = React.useState(0);
  const [captchaStatus, setCaptchaStatus] = React.useState<TurnstileWidgetStatus>("loading");
  const captchaRequired = options.captcha.provider === "turnstile";
  const captchaUnavailable = captchaRequired && captchaStatus === "unavailable";
  const errorMessage = captchaUnavailable
    ? t("errors.captcha-unavailable")
    : error?.legacy
      ? legacyT(sub2ApiFailureMessageKeys[error.reason])
      : error ? t(`errors.${error.reason}`) : "";

  React.useEffect(() => {
    setPassword("");
    setCode("");
    setCaptchaToken("");
  }, [stage.stage]);

  const submitPassword = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting || !email.trim() || !password || (captchaRequired && (!captchaToken || captchaStatus !== "verified"))) return;
    const captcha: Sub2ApiPasswordCaptcha = captchaRequired ? { provider: "turnstile", token: captchaToken } : { provider: "none" };
    void onPasswordSubmit(email, password, captcha);
    setPassword("");
    setCaptchaToken("");
    setCaptchaResetSignal((value) => value + 1);
  };

  const submitRemoteCode = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting || !/^\d{6}$/.test(code)) return;
    void onRemoteCodeSubmit(code);
    setCode("");
  };

  return (
    <div className="mt-7" data-sub2api-password-login="">
      {stage.stage === "protocol-selection" ? (
        <Sub2ApiProtocolSelection selection={stage.selection} onSubmit={(decisions) => { void onProtocolSubmit(decisions); }} onCancel={onCancel} cancelLabel={t("cancel")} />
      ) : stage.stage === "remote-2fa" ? (
        <>
          <div className="space-y-2 text-center">
            <h1 className="text-xl font-semibold leading-7">{t("remoteTwoFactorTitle")}</h1>
            <p className="text-sm leading-6 text-muted-foreground">{t("remoteTwoFactorDescription", { email: stage.maskedEmail })}</p>
          </div>
          <form className="mt-5 space-y-4" onSubmit={submitRemoteCode}>
            <div className="space-y-2">
              <label className="text-sm font-medium" htmlFor="sub2api-remote-otp">{t("remoteTwoFactorCode")}</label>
              <Input
                id="sub2api-remote-otp"
                name="sub2api_remote_otp"
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                maxLength={6}
                placeholder={t("remoteTwoFactorPlaceholder")}
                value={code}
                onChange={(event) => setCode(event.target.value.replace(/\D/g, "").slice(0, 6))}
                disabled={submitting}
                required
              />
            </div>
            <Button type="submit" className="h-9 w-full" disabled={submitting || code.length !== 6}>
              {submitting ? <SpinnerLabel>{t("verifying")}</SpinnerLabel> : t("verifyRemote")}
            </Button>
          </form>
          <Button type="button" variant="ghost" className="mt-2 w-full" onClick={onCancel}>{t("cancel")}</Button>
        </>
      ) : stage.stage === "form" ? (
        <>
          <div className="space-y-2 text-center">
            <h1 className="text-xl font-semibold leading-7">{t("title")}</h1>
            <p className="text-sm leading-6 text-muted-foreground">{t("description")}</p>
          </div>
          <form className="mt-5 space-y-4" onSubmit={submitPassword} data-sub2api-password-form="">
            <div className="space-y-2">
              <label className="text-sm font-medium" htmlFor="sub2api-email">{t("email")}</label>
              <Input
                id="sub2api-email"
                name="sub2api_email"
                type="email"
                autoComplete="off"
                maxLength={254}
                placeholder={t("emailPlaceholder")}
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                disabled={submitting}
                required
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium" htmlFor="sub2api-password">{t("password")}</label>
              <Input
                id="sub2api-password"
                name="sub2api_password"
                type="password"
                autoComplete="off"
                maxLength={1024}
                placeholder={t("password")}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                disabled={submitting}
                required
              />
            </div>
            {captchaRequired ? (
              <TurnstileWidget
                siteKey={options.captcha.siteKey}
                resetSignal={captchaResetSignal}
                onTokenChange={setCaptchaToken}
                onStatusChange={setCaptchaStatus}
              />
            ) : null}
            <Button
              type="submit"
              className="h-9 w-full"
              disabled={submitting || !email.trim() || !password || (captchaRequired && (!captchaToken || captchaStatus !== "verified"))}
            >
              {submitting ? <SpinnerLabel>{t("signingIn")}</SpinnerLabel> : t("signIn")}
            </Button>
          </form>
        </>
      ) : null}

      {errorMessage ? <p className="mt-4 break-words text-center text-sm leading-6 text-destructive" role="alert">{errorMessage}</p> : null}
      {errorSummary ? <Sub2ApiImportSummaryDetails summary={createSub2ApiImportSummaryView(errorSummary)} /> : null}
      <p className="mt-4 text-center text-xs leading-5 text-muted-foreground">{t("boundary")}</p>
      <Button type="button" variant="link" className="mt-1 h-auto w-full text-xs" onClick={() => { void openExternal(options.fallbackURL); }}>
        {t("openSub2Api")}
      </Button>
    </div>
  );
}
