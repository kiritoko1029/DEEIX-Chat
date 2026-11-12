"use client";

import * as React from "react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { Sub2ApiProtocol, Sub2ApiProtocolSelection as Sub2ApiProtocolSelectionData } from "@/shared/api/auth-types";
import { isSub2ApiProtocol, type Sub2ApiProtocolDecision } from "@/shared/auth/sub2api-protocol";

const PROTOCOL_LABELS: Record<Sub2ApiProtocol, { label: string; endpoint: string }> = {
  openai_responses: { label: "OpenAI Responses", endpoint: "/v1/responses" },
  anthropic_messages: { label: "Anthropic Messages", endpoint: "/v1/messages" },
  openai_chat_completions: { label: "OpenAI Chat Completions", endpoint: "/v1/chat/completions" },
};

type Sub2ApiProtocolSelectionProps = {
  selection: Sub2ApiProtocolSelectionData;
  onSubmit: (decisions: ReadonlyMap<number, Sub2ApiProtocolDecision>) => void;
  onCancel: () => void;
};

export function Sub2ApiProtocolSelection({ selection, onSubmit, onCancel }: Sub2ApiProtocolSelectionProps) {
  const t = useTranslations("login.sub2api.protocolSelection");
  const [decisions, setDecisions] = React.useState<ReadonlyMap<number, Sub2ApiProtocolDecision>>(new Map());
  const currentDecisions = new Map(selection.items.flatMap((item) => {
    const decision = decisions.get(item.keyID);
    return decision ? [[item.keyID, decision] as const] : [];
  }));
  const complete = selection.items.every((item) => currentDecisions.has(item.keyID));

  const updateDecision = (keyID: number, value: string) => {
    if (value !== "skip" && !isSub2ApiProtocol(value)) return;
    const next = new Map(currentDecisions);
    next.set(keyID, value);
    setDecisions(next);
  };

  const applyToUnselected = (decision: Sub2ApiProtocolDecision) => {
    const next = new Map(currentDecisions);
    for (const item of selection.items) {
      if (!next.has(item.keyID)) next.set(item.keyID, decision);
    }
    setDecisions(next);
  };

  return (
    <Card className="gap-4 py-5 shadow-none">
      <CardHeader className="px-5">
        <CardTitle className="text-xl leading-7">{t("title")}</CardTitle>
        <CardDescription className="text-sm leading-6">{t("description")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 px-5">
        <div className="space-y-2" role="group" aria-label={t("bulkActions")}>
          <p className="text-xs font-medium text-muted-foreground">{t("bulkActions")}</p>
          <div className="flex flex-wrap gap-2">
            {selection.protocols.map((protocol) => (
              <Button key={protocol} type="button" variant="outline" size="sm" onClick={() => applyToUnselected(protocol)}>
                {t("applyToUnselected", { protocol: PROTOCOL_LABELS[protocol].label })}
              </Button>
            ))}
            <Button type="button" variant="outline" size="sm" onClick={() => applyToUnselected("skip")}>
              {t("skipUnselected")}
            </Button>
          </div>
        </div>
        <p className="text-xs text-muted-foreground" aria-live="polite">
          {t("progress", { selected: currentDecisions.size, total: selection.items.length })}
        </p>
        <ul className="max-h-[min(48vh,26rem)] space-y-3 overflow-y-auto overscroll-contain pr-1">
          {selection.items.map((item) => {
            const name = item.name || t("unnamedKey");
            return (
              <li key={item.keyID} className="rounded-lg border border-border/70 bg-muted/20 p-3">
                <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(220px,0.9fr)] sm:items-center">
                  <div className="min-w-0">
                    <p className="break-words text-sm font-medium leading-5">{name} <span className="text-muted-foreground">#{item.keyID}</span></p>
                    <dl className="mt-1 space-y-0.5 text-xs leading-5 text-muted-foreground">
                      <div className="flex flex-wrap gap-x-1.5"><dt>{t("group")}</dt><dd className="break-words">{item.groupName || t("notProvided")}</dd></div>
                      <div className="flex flex-wrap gap-x-1.5"><dt>{t("platform")}</dt><dd className="break-words">{item.platform || t("notProvided")}</dd></div>
                    </dl>
                  </div>
                  <Select value={currentDecisions.get(item.keyID) ?? ""} onValueChange={(value) => updateDecision(item.keyID, value)}>
                    <SelectTrigger className="h-9 bg-background" aria-label={t("keyProtocol", { name: `${name} #${item.keyID}` })}>
                      <SelectValue placeholder={t("chooseProtocol")} />
                    </SelectTrigger>
                    <SelectContent position="popper">
                      {selection.protocols.map((protocol) => (
                        <SelectItem key={protocol} value={protocol}>
                          <span>{PROTOCOL_LABELS[protocol].label}</span>
                          <span className="font-mono text-[11px] text-muted-foreground">{PROTOCOL_LABELS[protocol].endpoint}</span>
                        </SelectItem>
                      ))}
                      <SelectItem value="skip">{t("skipOnce")}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </li>
            );
          })}
        </ul>
      </CardContent>
      <CardFooter className="flex-col-reverse gap-2 px-5 sm:flex-row sm:justify-end">
        <Button type="button" variant="ghost" className="w-full sm:w-auto" onClick={onCancel}>{t("cancel")}</Button>
        <Button
          type="button"
          className="w-full sm:w-auto"
          data-sub2api-protocol-confirm=""
          disabled={!complete}
          onClick={() => { if (complete) onSubmit(currentDecisions); }}
        >
          {t("confirm")}
        </Button>
      </CardFooter>
    </Card>
  );
}
