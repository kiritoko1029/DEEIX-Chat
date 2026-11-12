import assert from "node:assert/strict";
import test from "node:test";

import {
  createSub2ApiProtocolExchangeOptions,
  mergeSub2ApiProtocolExchangeOptions,
  parseSub2ApiProtocolSelection,
  SUB2API_PROTOCOL_SELECTION_MAX_ITEMS,
  SUB2API_PROTOCOLS,
} from "./sub2api-protocol.ts";

const details = {
  protocolSelection: {
    items: [
      { keyID: 11, name: "Unknown A", groupName: "Custom", platform: "deepseek" },
      { keyID: 12, name: "Unknown B", groupName: "Custom", platform: "other" },
    ],
    protocols: [...SUB2API_PROTOCOLS],
  },
};

test("accepts only the protocol-required 409 and preserves bounded display metadata", () => {
  assert.deepEqual(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", details), details.protocolSelection);
  assert.equal(parseSub2ApiProtocolSelection(400, "auth.sub2api_protocol_required", details), null);
  assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_rejected", details), null);
  assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", null), null);
});

test("rejects unknown or duplicate protocols and duplicate or invalid Key IDs", () => {
  for (const protocols of [["unknown"], ["openai_responses", "openai_responses"], []]) {
    assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", {
      protocolSelection: { items: details.protocolSelection.items, protocols },
    }), null);
  }
  for (const keyID of [0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, "11"]) {
    assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", {
      protocolSelection: { items: [{ ...details.protocolSelection.items[0], keyID }], protocols: details.protocolSelection.protocols },
    }), null);
  }
  assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", {
    protocolSelection: {
      items: [details.protocolSelection.items[0], details.protocolSelection.items[0]],
      protocols: details.protocolSelection.protocols,
    },
  }), null);
});

test("rejects invalid, overlong, controlled, or credential-bearing metadata", () => {
  const invalidItems = [
    { ...details.protocolSelection.items[0], name: "x".repeat(129) },
    { ...details.protocolSelection.items[0], groupName: "bad\nname" },
    { ...details.protocolSelection.items[0], platform: "bad\u200dplatform" },
    { ...details.protocolSelection.items[0], name: 42 },
    { ...details.protocolSelection.items[0], apiKey: "must-not-be-accepted" },
  ];
  for (const item of invalidItems) {
    assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", {
      protocolSelection: { items: [item], protocols: details.protocolSelection.protocols },
    }), null);
  }
  assert.equal(parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", {
    protocolSelection: {
      items: Array.from({ length: SUB2API_PROTOCOL_SELECTION_MAX_ITEMS + 1 }, (_, index) => ({ ...details.protocolSelection.items[0], keyID: index + 1 })),
      protocols: details.protocolSelection.protocols,
    },
  }), null);
});

test("builds explicit mixed protocol and skip payloads without raw Keys or URLs", () => {
  const selection = parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", details);
  assert(selection);
  const options = createSub2ApiProtocolExchangeOptions(selection, new Map([
    [11, "openai_responses"],
    [12, "skip"],
  ]));
  assert.deepEqual(options, {
    protocolSelections: [{ keyID: 11, protocol: "openai_responses" }],
    skipKeyIDs: [12],
  });
  const serialized = JSON.stringify(options);
  assert.equal(serialized.includes("token"), false);
  assert.equal(serialized.includes("baseURL"), false);
  assert.equal(serialized.includes("apiKey"), false);
});

test("requires exactly one current decision for every pending Key", () => {
  const selection = parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", details);
  assert(selection);
  assert.equal(createSub2ApiProtocolExchangeOptions(selection, new Map([[11, "openai_responses"]])), null);
  assert.equal(createSub2ApiProtocolExchangeOptions(selection, new Map([
    [11, "openai_responses"],
    [12, "anthropic_messages"],
    [13, "skip"],
  ])), null);
  assert.equal(createSub2ApiProtocolExchangeOptions(selection, new Map([
    [11, "openai_responses"],
    [12, "unknown"],
  ])), null);
  const restricted = { ...selection, protocols: ["openai_responses" as const] };
  assert.equal(createSub2ApiProtocolExchangeOptions(restricted, new Map([
    [11, "openai_responses"],
    [12, "anthropic_messages"],
  ])), null);
});

test("carries earlier confirmed decisions into a repeated 409 without duplicate IDs", () => {
  const selection = parseSub2ApiProtocolSelection(409, "auth.sub2api_protocol_required", details);
  assert(selection);
  const current = createSub2ApiProtocolExchangeOptions(selection, new Map([
    [11, "anthropic_messages"],
    [12, "skip"],
  ]));
  assert(current);
  const previous = new Map<number, "openai_responses" | "anthropic_messages">([
    [99, "openai_responses"],
    [11, "openai_responses"],
  ]);
  assert.deepEqual(mergeSub2ApiProtocolExchangeOptions(previous, current, SUB2API_PROTOCOLS), {
    protocolSelections: [
      { keyID: 99, protocol: "openai_responses" },
      { keyID: 11, protocol: "anthropic_messages" },
    ],
    skipKeyIDs: [12],
  });
  assert.equal(mergeSub2ApiProtocolExchangeOptions(previous, current, ["openai_responses"]), null);
  assert.equal(mergeSub2ApiProtocolExchangeOptions(previous, {
    protocolSelections: [{ keyID: 11, protocol: "openai_responses" }],
    skipKeyIDs: [11],
  }, SUB2API_PROTOCOLS), null);
});
