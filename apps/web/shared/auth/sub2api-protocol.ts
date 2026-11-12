import type {
  Sub2ApiExchangeOptions,
  Sub2ApiProtocol,
  Sub2ApiProtocolSelection,
  Sub2ApiProtocolSelectionItem,
} from "../api/auth-types.ts";

export const SUB2API_PROTOCOL_SELECTION_MAX_ITEMS = 2000;
export const SUB2API_PROTOCOL_METADATA_MAX_LENGTH = 128;
export const SUB2API_PROTOCOLS: readonly Sub2ApiProtocol[] = [
  "openai_responses",
  "anthropic_messages",
  "openai_chat_completions",
];

export type Sub2ApiProtocolDecision = Sub2ApiProtocol | "skip";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOnlyKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  return Object.keys(value).every((key) => keys.includes(key));
}

function isValidKeyID(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

export function isSub2ApiProtocol(value: unknown): value is Sub2ApiProtocol {
  return typeof value === "string" && SUB2API_PROTOCOLS.some((protocol) => protocol === value);
}

function parseMetadata(value: unknown): string | null {
  if (typeof value !== "string" || Array.from(value).length > SUB2API_PROTOCOL_METADATA_MAX_LENGTH) return null;
  if (/[\p{Cc}\p{Cf}]/u.test(value)) return null;
  return value.trim();
}

function parseItem(value: unknown): Sub2ApiProtocolSelectionItem | null {
  if (!isRecord(value) || !hasOnlyKeys(value, ["keyID", "name", "groupName", "platform"])) return null;
  if (!isValidKeyID(value.keyID)) return null;
  const name = parseMetadata(value.name);
  const groupName = parseMetadata(value.groupName);
  const platform = parseMetadata(value.platform);
  if (name === null || groupName === null || platform === null) return null;
  return { keyID: value.keyID, name, groupName, platform };
}

export function parseSub2ApiProtocolSelection(
  status: number,
  errorCode: unknown,
  details: unknown,
): Sub2ApiProtocolSelection | null {
  if (status !== 409 || errorCode !== "auth.sub2api_protocol_required" || !isRecord(details)) return null;
  const selection = details.protocolSelection;
  if (!isRecord(selection) || !hasOnlyKeys(selection, ["items", "protocols"])) return null;
  if (!Array.isArray(selection.items) || selection.items.length === 0 || selection.items.length > SUB2API_PROTOCOL_SELECTION_MAX_ITEMS) return null;
  if (!Array.isArray(selection.protocols) || selection.protocols.length === 0 || selection.protocols.length > SUB2API_PROTOCOLS.length) return null;

  const protocols: Sub2ApiProtocol[] = [];
  for (const protocol of selection.protocols) {
    if (!isSub2ApiProtocol(protocol) || protocols.includes(protocol)) return null;
    protocols.push(protocol);
  }
  const items: Sub2ApiProtocolSelectionItem[] = [];
  const keyIDs = new Set<number>();
  for (const value of selection.items) {
    const item = parseItem(value);
    if (!item || keyIDs.has(item.keyID)) return null;
    keyIDs.add(item.keyID);
    items.push(item);
  }
  return { items, protocols };
}

export function createSub2ApiProtocolExchangeOptions(
  selection: Sub2ApiProtocolSelection,
  decisions: ReadonlyMap<number, unknown>,
): Sub2ApiExchangeOptions | null {
  if (selection.items.length === 0 || selection.items.length > SUB2API_PROTOCOL_SELECTION_MAX_ITEMS || decisions.size !== selection.items.length) return null;
  const protocolSelections: Sub2ApiExchangeOptions["protocolSelections"] = [];
  const skipKeyIDs: number[] = [];
  const seen = new Set<number>();
  for (const item of selection.items) {
    if (!isValidKeyID(item.keyID) || seen.has(item.keyID)) return null;
    seen.add(item.keyID);
    const decision = decisions.get(item.keyID);
    if (decision === "skip") {
      skipKeyIDs.push(item.keyID);
    } else if (decision && isSub2ApiProtocol(decision) && selection.protocols.includes(decision)) {
      protocolSelections.push({ keyID: item.keyID, protocol: decision });
    } else {
      return null;
    }
  }
  return { protocolSelections, skipKeyIDs };
}

export function mergeSub2ApiProtocolExchangeOptions(
  previous: ReadonlyMap<number, Sub2ApiProtocolDecision>,
  current: Sub2ApiExchangeOptions,
  protocols: readonly Sub2ApiProtocol[],
): Sub2ApiExchangeOptions | null {
  if (!Array.isArray(current.protocolSelections) || !Array.isArray(current.skipKeyIDs)) return null;
  const merged = new Map<number, Sub2ApiProtocolDecision>();
  for (const [keyID, decision] of previous) {
    if (!isValidKeyID(keyID) || (decision !== "skip" && (!isSub2ApiProtocol(decision) || !protocols.includes(decision)))) return null;
    merged.set(keyID, decision);
  }
  const currentIDs = new Set<number>();
  for (const choice of current.protocolSelections) {
    if (!isRecord(choice) || !hasOnlyKeys(choice, ["keyID", "protocol"]) || !isValidKeyID(choice.keyID)) return null;
    if (!isSub2ApiProtocol(choice.protocol) || !protocols.includes(choice.protocol) || currentIDs.has(choice.keyID)) return null;
    currentIDs.add(choice.keyID);
    merged.set(choice.keyID, choice.protocol);
  }
  for (const keyID of current.skipKeyIDs) {
    if (!isValidKeyID(keyID) || currentIDs.has(keyID)) return null;
    currentIDs.add(keyID);
    merged.set(keyID, "skip");
  }
  if (merged.size > SUB2API_PROTOCOL_SELECTION_MAX_ITEMS) return null;
  const protocolSelections: Sub2ApiExchangeOptions["protocolSelections"] = [];
  const skipKeyIDs: number[] = [];
  for (const [keyID, decision] of merged) {
    if (decision === "skip") skipKeyIDs.push(keyID);
    else protocolSelections.push({ keyID, protocol: decision });
  }
  return { protocolSelections, skipKeyIDs };
}
