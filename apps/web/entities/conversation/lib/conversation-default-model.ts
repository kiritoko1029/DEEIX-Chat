import { getConversationDefaultModelCandidate } from "@/shared/api/conversation";
import { listPublicModels } from "@/shared/api/model";
import type { PublicModelDTO } from "@/shared/api/model-types";
import { loadUserSettingsSnapshot } from "@/entities/user-settings";
import { findExactModelRef } from "@/entities/model";

export type ConversationDefaultModelSource =
  | "explicit"
  | "project_default"
  | "user_default"
  | "system_default"
  | "recommended"
  | "none";

export type ConversationDefaultModelResult = {
  platformModelName: string;
  source: ConversationDefaultModelSource;
};

type ResolveConversationDefaultModelInput = {
  accessToken: string;
  explicitModel?: string;
  projectDefaultModel?: string;
  availableModels?: PublicModelDTO[];
  userDefaultModel?: string;
};

export async function resolveConversationDefaultModel({
  accessToken,
  explicitModel,
  projectDefaultModel,
  availableModels,
  userDefaultModel,
}: ResolveConversationDefaultModelInput): Promise<ConversationDefaultModelResult> {
  const models = availableModels ?? await listPublicModels(accessToken);
  const explicit = findExactModelRef(models, explicitModel ?? "");
  if (explicit) {
    return { platformModelName: explicit, source: "explicit" };
  }

  const projectDefault = findExactModelRef(models, projectDefaultModel ?? "");
  if (projectDefault) {
    return { platformModelName: projectDefault, source: "project_default" };
  }

  const defaultModel = userDefaultModel
    ?? (await loadUserSettingsSnapshot(accessToken))["chat.default_model"];
  const userDefault = findExactModelRef(models, defaultModel ?? "");
  if (userDefault) {
    return { platformModelName: userDefault, source: "user_default" };
  }

  const candidate = await getConversationDefaultModelCandidate(accessToken).catch((): null => null);
  const candidateModel = findExactModelRef(models, candidate?.platformModelName ?? "");
  if (candidateModel) {
    return {
      platformModelName: candidateModel,
      source: "system_default",
    };
  }

  const recommended = models[0]?.platformModelName?.trim() ?? "";
  return recommended
    ? { platformModelName: recommended, source: "recommended" }
    : { platformModelName: "", source: "none" };
}
