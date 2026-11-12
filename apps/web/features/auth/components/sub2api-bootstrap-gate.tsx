"use client";

import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { SpinnerLabel } from "@/components/ui/spinner";
import { useAuthSub2ApiBootstrap } from "@/features/auth/hooks/use-auth-sub2api-bootstrap";
import { AppLogo } from "@/shared/components/app-logo";
import { sub2ApiFailureMessageKeys } from "@/shared/auth/sub2api-failure";
import { Sub2ApiImportSummaryDetails } from "@/features/auth/components/sub2api-import-summary-details";
import { createSub2ApiImportSummaryView } from "@/features/auth/model/sub2api-import-summary";

export function Sub2ApiBootstrapGate({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const t = useTranslations("login.sub2api");
  const { state, returnToLogin } = useAuthSub2ApiBootstrap();

  if (state.status === "ready" || (state.status === "handoff" && pathname !== "/")) {
    return <>{children}</>;
  }

  if (state.status === "checking" || state.status === "handoff") {
    return null;
  }

  const errorMessage = state.status === "error" ? t(sub2ApiFailureMessageKeys[state.reason]) : "";

  return (
    <main className="flex min-h-screen items-center justify-center px-4 py-8 text-foreground">
      <div className="w-full max-w-[360px]">
        <div className="flex flex-col items-center text-center">
          <AppLogo width={32} height={32} priority className="h-9 w-auto" />
        </div>
        {state.status === "error" ? (
          <div className="mt-7 space-y-5 text-center" role="alert">
            <div className="space-y-2">
              <h1 className="text-xl font-semibold leading-7">{t("errorTitle")}</h1>
              <p className="break-words text-sm leading-6 text-muted-foreground">{errorMessage}</p>
            </div>
            {state.summary ? <Sub2ApiImportSummaryDetails summary={createSub2ApiImportSummaryView(state.summary)} /> : null}
            <Button
              type="button"
              className="h-9 w-full rounded-md bg-foreground text-sm font-semibold text-background shadow-none hover:bg-foreground/90"
              onClick={returnToLogin}
            >
              {t("backToLogin")}
            </Button>
          </div>
        ) : (
          <div className="mt-7 flex flex-col items-center gap-3 text-center text-sm text-muted-foreground" aria-live="polite">
            <SpinnerLabel>{t("loading")}</SpinnerLabel>
            <p>{t("loadingDescription")}</p>
          </div>
        )}
      </div>
    </main>
  );
}
