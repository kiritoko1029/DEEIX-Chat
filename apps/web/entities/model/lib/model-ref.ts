const PERSONAL_MODEL_REF = /^personal:([a-z0-9]{8,32})\/(.+)$/;

export type PersonalModelRef = {
  providerID: string;
  modelName: string;
};

export function parsePersonalModelRef(platformModelName: string): PersonalModelRef | null {
  const match = PERSONAL_MODEL_REF.exec(platformModelName.trim());
  return match ? { providerID: match[1], modelName: match[2] } : null;
}

export function findExactModelRef(
  models: readonly { platformModelName: string }[],
  platformModelName: string,
): string {
  const normalizedName = platformModelName.trim();
  if (!normalizedName) return "";
  return models.some((item) => item.platformModelName === normalizedName) ? normalizedName : "";
}
