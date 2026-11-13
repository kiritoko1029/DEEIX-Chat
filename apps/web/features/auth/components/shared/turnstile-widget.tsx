"use client";

import * as React from "react";
import Script from "next/script";

type TurnstileAPI = {
  render: (
    container: HTMLElement,
    options: {
      sitekey: string;
      theme?: "auto" | "light" | "dark";
      size?: "normal" | "compact" | "flexible";
      callback?: (token: string) => void;
      "expired-callback"?: () => void;
      "error-callback"?: () => void;
    },
  ) => string;
  reset: (widgetID?: string) => void;
  remove?: (widgetID: string) => void;
};

declare global {
  // biome-ignore lint/style/useConsistentTypeDefinitions: augmenting the global Window requires interface declaration merging.
  interface Window {
    turnstile?: TurnstileAPI;
  }
}

export type TurnstileWidgetStatus = "loading" | "ready" | "verified" | "unavailable";

type TurnstileWidgetProps = {
  siteKey: string;
  resetSignal: number;
  onTokenChange: (token: string) => void;
  onStatusChange?: (status: TurnstileWidgetStatus) => void;
  initializationTimeoutMs?: number;
};

export function TurnstileWidget({ siteKey, resetSignal, onTokenChange, onStatusChange, initializationTimeoutMs = 15_000 }: TurnstileWidgetProps) {
  const containerRef = React.useRef<HTMLDivElement | null>(null);
  const widgetIDRef = React.useRef<string | null>(null);
  const onTokenChangeRef = React.useRef(onTokenChange);
  const onStatusChangeRef = React.useRef(onStatusChange);
  const lastResetSignalRef = React.useRef(resetSignal);
  const [scriptReady, setScriptReady] = React.useState(() => typeof window !== "undefined" && Boolean(window.turnstile));
  const [scriptFailed, setScriptFailed] = React.useState(false);

  React.useEffect(() => {
    onTokenChangeRef.current = onTokenChange;
    onStatusChangeRef.current = onStatusChange;
  }, [onStatusChange, onTokenChange]);

  React.useEffect(() => {
    if (!onStatusChange || scriptReady || scriptFailed) return undefined;
    onStatusChangeRef.current?.("loading");
    const timer = window.setTimeout(() => setScriptFailed(true), initializationTimeoutMs);
    return () => window.clearTimeout(timer);
  }, [initializationTimeoutMs, onStatusChange, scriptFailed, scriptReady]);

  React.useEffect(() => {
    if (scriptFailed) onStatusChangeRef.current?.("unavailable");
  }, [scriptFailed]);

  React.useEffect(() => {
    const container = containerRef.current;
    if (!siteKey || !scriptReady || scriptFailed || !container || !window.turnstile || widgetIDRef.current) {
      return undefined;
    }

    try {
      widgetIDRef.current = window.turnstile.render(container, {
        sitekey: siteKey,
        theme: "auto",
        size: "flexible",
        callback: (token) => {
          onTokenChangeRef.current(token);
          onStatusChangeRef.current?.("verified");
        },
        "expired-callback": () => {
          onTokenChangeRef.current("");
          onStatusChangeRef.current?.("ready");
        },
        "error-callback": () => {
          onTokenChangeRef.current("");
          onStatusChangeRef.current?.("unavailable");
        },
      });
      onStatusChangeRef.current?.("ready");
    } catch {
      onTokenChangeRef.current("");
      onStatusChangeRef.current?.("unavailable");
    }

    return () => {
      const widgetID = widgetIDRef.current;
      if (widgetID && window.turnstile?.remove) {
        window.turnstile.remove(widgetID);
      }
      widgetIDRef.current = null;
      onTokenChangeRef.current("");
    };
  }, [scriptFailed, scriptReady, siteKey]);

  React.useEffect(() => {
    if (lastResetSignalRef.current === resetSignal) {
      return;
    }
    lastResetSignalRef.current = resetSignal;
    onTokenChangeRef.current("");
    if (widgetIDRef.current && window.turnstile) {
      try {
        onStatusChangeRef.current?.("ready");
        window.turnstile.reset(widgetIDRef.current);
      } catch {
        onStatusChangeRef.current?.("unavailable");
      }
    }
  }, [resetSignal]);

  return (
    <div className="min-h-[65px] overflow-hidden rounded-md" data-turnstile-status={scriptFailed ? "unavailable" : undefined}>
      <Script
        id="cloudflare-turnstile"
        src="https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit"
        strategy="afterInteractive"
        async
        defer
        onLoad={() => setScriptReady(true)}
        onReady={() => setScriptReady(true)}
        onError={() => setScriptFailed(true)}
      />
      <div ref={containerRef} />
    </div>
  );
}
