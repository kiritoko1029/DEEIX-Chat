export const SUB2API_TOKEN_MAX_LENGTH = 16384;

export type Sub2ApiCredentials = {
  userID: number;
  token: string;
};

export type Sub2ApiLinkError = "duplicate" | "missing" | "invalid-user-id" | "invalid-token";

export type Sub2ApiLinkParseResult =
  | { kind: "none" }
  | { kind: "exchange"; credentials: Sub2ApiCredentials; cleanedURL: string }
  | { kind: "error"; reason: Sub2ApiLinkError; cleanedURL: string };

type Sub2ApiLocation = {
  pathname: string;
  search: string;
  hash: string;
};

function hasSub2ApiParams(params: URLSearchParams): boolean {
  return params.has("user_id") || params.has("token");
}

function cleanParams(params: URLSearchParams): void {
  params.delete("user_id");
  params.delete("token");
}

export function parseSub2ApiLink(location: Sub2ApiLocation): Sub2ApiLinkParseResult {
  if (location.pathname !== "/") {
    return { kind: "none" };
  }

  const query = new URLSearchParams(location.search);
  const fragment = new URLSearchParams(location.hash.startsWith("#") ? location.hash.slice(1) : location.hash);
  const queryHasParams = hasSub2ApiParams(query);
  const fragmentHasParams = hasSub2ApiParams(fragment);
  if (!queryHasParams && !fragmentHasParams) {
    return { kind: "none" };
  }

  const userIDs = [...query.getAll("user_id"), ...fragment.getAll("user_id")];
  const tokens = [...query.getAll("token"), ...fragment.getAll("token")];
  if (queryHasParams) {
    cleanParams(query);
  }
  if (fragmentHasParams) {
    cleanParams(fragment);
  }

  const search = queryHasParams ? query.toString() : location.search.replace(/^\?/, "");
  const hash = fragmentHasParams
    ? fragment.toString()
    : location.hash.replace(/^#/, "");
  const cleanedURL = `/${search ? `?${search}` : ""}${hash ? `#${hash}` : ""}`;

  if (userIDs.length > 1 || tokens.length > 1) {
    return { kind: "error", reason: "duplicate", cleanedURL };
  }
  if (userIDs.length === 0 || tokens.length === 0) {
    return { kind: "error", reason: "missing", cleanedURL };
  }

  const rawUserID = userIDs[0];
  const userID = Number(rawUserID);
  if (!/^[1-9][0-9]*$/.test(rawUserID) || !Number.isSafeInteger(userID)) {
    return { kind: "error", reason: "invalid-user-id", cleanedURL };
  }

  const token = tokens[0];
  if (!token || token.length > SUB2API_TOKEN_MAX_LENGTH) {
    return { kind: "error", reason: "invalid-token", cleanedURL };
  }

  return { kind: "exchange", credentials: { userID, token }, cleanedURL };
}
