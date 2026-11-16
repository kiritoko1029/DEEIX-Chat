"use client";

import { ChevronRight, Search } from "lucide-react";

import {
  ToolSourceFavicon,
  type WebSearchAggregate,
} from "@/features/chat/components/message/message-agent-tool-step";
import type { ProcessTraceLabels } from "@/features/chat/hooks/use-chat-trace-labels";
import { cn } from "@/lib/utils";

const PREVIEW_FAVICON_COUNT = 3;

export type MessageWebSearchSummaryProps = {
  summary: WebSearchAggregate;
  labels: ProcessTraceLabels;
  streaming: boolean;
  // Opens the same results in the side panel; absent when the row cannot host a panel.
  onOpen?: () => void;
};

/**
 * One-line summary for a whole turn's web searches, placed above the answer. Clicking it opens every
 * unique page the turn searched or read in the side panel, so results no longer live in tool rows.
 */
export function MessageWebSearchSummary({
  summary,
  labels,
  streaming,
  onOpen,
}: MessageWebSearchSummaryProps) {
  const sourceCount = summary.sources.length;
  const callCount = summary.searchCount + summary.readCount;
  if (callCount === 0) {
    return null;
  }

  const pending = streaming && summary.pending && sourceCount === 0;
  const label = pending
    ? labels.tool.detail.webSearching
    : summary.searchCount > 0
      ? labels.tool.detail.webSearchSummary(sourceCount)
      : labels.tool.detail.webReadSummary(sourceCount);
  const previewIcons = summary.sources
    .filter((source) => Boolean(source.siteIcon))
    .slice(0, PREVIEW_FAVICON_COUNT);
  const expandable = sourceCount > 0 && Boolean(onOpen);

  return (
    <div className="mb-2 w-full">
      <button
        type="button"
        onClick={expandable ? onOpen : undefined}
        aria-label={expandable ? labels.tool.detail.openWebSourcesPanel : undefined}
        disabled={!expandable}
        className={cn(
          "inline-flex max-w-full items-center gap-1.5 rounded-full border border-border/35 bg-muted/25 py-1 pl-2.5 pr-2 text-[12px] leading-5 text-muted-foreground/80 transition-colors",
          expandable ? "cursor-pointer hover:bg-muted/40 hover:text-foreground/85" : "cursor-default",
        )}
      >
        <Search className={cn("size-3.5 shrink-0", pending && "animate-pulse")} strokeWidth={1.8} aria-hidden="true" />
        <span className="truncate">{label}</span>
        {previewIcons.length > 0 ? (
          <span className="-space-x-1 flex shrink-0 items-center" aria-hidden="true">
            {previewIcons.map((source, index) => (
              <ToolSourceFavicon key={`${source.url}-${index}`} src={source.siteIcon} />
            ))}
          </span>
        ) : null}
        {expandable ? (
          <ChevronRight className="size-3.5 shrink-0" strokeWidth={1.8} aria-hidden="true" />
        ) : null}
      </button>
    </div>
  );
}
