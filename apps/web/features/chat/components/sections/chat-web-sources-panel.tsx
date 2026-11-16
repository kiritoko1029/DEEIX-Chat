"use client";

import { Search, X } from "lucide-react";
import { useLocale } from "next-intl";
import * as React from "react";

import { Button } from "@/components/ui/button";
import {
  buildWebSearchAggregate,
  ToolSearchSources,
} from "@/features/chat/components/message/message-agent-tool-step";
import { buildTraceDisplayEvents } from "@/features/chat/components/message/message-agent-trace";
import { useChatTraceLabels } from "@/features/chat/hooks/use-chat-trace-labels";
import type { ChatAreaMessage } from "@/features/chat/types/messages";

const WEB_SOURCES_PANEL_LIMIT = 200;

type ChatWebSourcesPanelProps = {
  // Undefined once the message is no longer shown (other branch, deleted).
  message: ChatAreaMessage | undefined;
  onClose: () => void;
};

/**
 * A turn's web search results in the side panel, so the page list stays open while reading the
 * answer and links can be opened beside the conversation instead of inside a long inline list.
 */
export function ChatWebSourcesPanel({ message, onClose }: ChatWebSourcesPanelProps) {
  const labels = useChatTraceLabels();
  const locale = useLocale();
  const trace = message?.processTrace;
  const summary = React.useMemo(() => {
    const events = (trace?.events ?? []).filter(
      (event) =>
        event.phase === "tools" ||
        event.phase === "upstream_think" ||
        event.eventType === "tool" ||
        event.eventType === "think",
    );
    return buildWebSearchAggregate(buildTraceDisplayEvents(events), trace?.tools);
  }, [trace]);
  const sourceCount = summary.sources.length;
  const callCount = summary.searchCount + summary.readCount;
  const streaming = Boolean(message?.isStreaming);
  const missing = !message;

  React.useEffect(() => {
    if (missing) {
      onClose();
    }
  }, [missing, onClose]);

  if (!message) {
    return null;
  }

  const label =
    callCount === 0 || (streaming && summary.pending && sourceCount === 0)
      ? labels.tool.detail.webSearching
      : summary.searchCount > 0
        ? labels.tool.detail.webSearchSummary(sourceCount)
        : labels.tool.detail.webReadSummary(sourceCount);

  return (
    <aside
      className="flex h-full min-h-0 w-full flex-col border-l border-border/55 bg-background"
      aria-label={labels.tool.detail.webSourcesPanelTitle}
    >
      <div className="flex h-12 shrink-0 items-center justify-between gap-2 border-b border-border/40 px-3">
        <h2 className="min-w-0 truncate text-sm font-semibold tracking-tight">
          {labels.tool.detail.webSourcesPanelTitle}
        </h2>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-7 shrink-0 rounded-md text-muted-foreground hover:bg-foreground/[0.04] hover:text-foreground"
          aria-label={labels.tool.detail.collapseWebSources}
          onClick={onClose}
        >
          <X className="size-4" strokeWidth={1.8} />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        <p className="mb-3 flex items-center gap-1.5 text-xs leading-5 text-muted-foreground">
          <Search className="size-3.5 shrink-0" strokeWidth={1.8} aria-hidden="true" />
          <span>{label}</span>
        </p>
        {sourceCount > 0 ? (
          <ToolSearchSources
            sources={summary.sources}
            labels={labels}
            locale={locale}
            limit={WEB_SOURCES_PANEL_LIMIT}
          />
        ) : null}
      </div>
    </aside>
  );
}
