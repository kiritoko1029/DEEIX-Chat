import type {
  ActiveSessionListResponse,
  ActiveSessionResponse,
  AuthUserIdentityProviderSummaryResponse,
  AuthUserResponse,
  DeleteAccountRequest,
  EmailRegistrationStartResponse,
  EmailVerificationStartResponse,
	IdentityProviderResponse,
  LoginOptionsResponse,
  LoginResponse,
  LogoutResponse,
  PasswordResetCompleteResponse,
  PasswordResetStartResponse,
  PatchMeRequest,
  UpdateCurrentSessionLocationRequest,
  ExternalProfileResponse,
  Sub2APIImportItemResponse,
  Sub2APIImportResponse,
} from "@deeix/api-contract";

export type UserIdentityProviderSummaryDTO = AuthUserIdentityProviderSummaryResponse;

export type Sub2ApiExternalProfile = ExternalProfileResponse;
export type Sub2ApiImportItem = Sub2APIImportItemResponse;
export type Sub2ApiImportSummary = Sub2APIImportResponse;

export type Sub2ApiProtocol = "openai_responses" | "anthropic_messages" | "openai_chat_completions";
export type Sub2ApiProtocolSelectionItem = {
  keyID: number;
  name: string;
  groupName: string;
  platform: string;
};
export type Sub2ApiProtocolSelection = {
  items: Sub2ApiProtocolSelectionItem[];
  protocols: Sub2ApiProtocol[];
};
export type Sub2ApiProtocolChoice = {
  keyID: number;
  protocol: Sub2ApiProtocol;
};
export type Sub2ApiExchangeOptions = {
  protocolSelections: Sub2ApiProtocolChoice[];
  skipKeyIDs: number[];
};
export type Sub2ApiPasswordCaptcha = {
  provider: "none" | "turnstile";
  token?: string;
};
export type Sub2ApiPasswordLoginOptions = {
  enabled: boolean;
  passwordLoginAvailable: boolean;
  browserOnly: true;
  unavailableReason: string | null;
  upstreamVersion: string;
  remoteTotpEnabled: boolean;
  captcha: {
    provider: "none" | "turnstile";
    siteKey: string;
  };
  fallbackURL: string;
};

export type UserDTO = Omit<AuthUserResponse, "externalProfile"> & {
  externalProfile?: Sub2ApiExternalProfile;
};

export type LoginData = Omit<LoginResponse, "user" | "verificationMethods" | "sub2apiImport"> & {
  user: UserDTO;
  twoFactorChallengeToken?: string;
  verificationMethods?: SecurityVerificationMethod[];
  sub2apiImport?: Sub2ApiImportSummary;
};

export type TwoFactorStatusData = {
  available: boolean;
  totpEnabled: boolean;
  required: boolean;
  recoveryCount: number;
  enabledAt: string | null;
};

export type TwoFactorSetupStartData = {
  secret: string;
  otpauthURL: string;
  expiresAt: string;
};

export type TwoFactorRecoveryCodesData = {
  recoveryCodes: string[];
  status: TwoFactorStatusData;
};

export type TwoFactorDisableData = {
  disabled: boolean;
};

export type SecurityVerificationMethod = "none" | "two_factor" | "email";

export type EmailRegistrationStartData = EmailRegistrationStartResponse & {
  debugCode?: string;
};

export type PasswordResetStartData = PasswordResetStartResponse;

export type PasswordResetCompleteData = PasswordResetCompleteResponse;

export type PasswordChangeVerificationStartData = Omit<EmailVerificationStartResponse, "availableMethods" | "verificationMethod"> & {
  verificationMethod: SecurityVerificationMethod;
  availableMethods: SecurityVerificationMethod[];
  debugCode?: string;
};

export type LoginPageSettings = {
  defaultNextPath: string;
};

export type IdentityProviderDTO = IdentityProviderResponse;

export type UserIdentityDTO = {
  id: number;
  providerID: number;
  providerType: "oidc" | "oauth2" | string;
  providerName: string;
  providerSlug: string;
  providerLogoURL?: string;
  providerDisplayName: string;
  email: string;
  emailVerified: boolean;
  linkedAt: string;
  lastLoginAt: string | null;
};

export type UserIdentityListData = {
  results: UserIdentityDTO[];
};

export type UserIdentityData = {
  identity: UserIdentityDTO;
};

export type LoginOptionsData = Omit<LoginOptionsResponse, "providers"> & {
  providers: IdentityProviderDTO[];
};

export type MeData = {
  user: UserDTO;
};

export type PatchMePayload = PatchMeRequest;

export type PatchUsernamePayload = {
  username: string;
};

export type ChangePasswordPayload = {
  currentPassword?: string;
  newPassword: string;
  verificationMethod?: SecurityVerificationMethod;
  code?: string;
};

export type ChangePasswordData = {
  changed: boolean;
};

export type CompleteOnboardingPayload = {
  newPassword?: string;
};

export type EmailVerificationStartData = Omit<EmailVerificationStartResponse, "availableMethods" | "verificationMethod"> & {
  verificationMethod: SecurityVerificationMethod;
  availableMethods: SecurityVerificationMethod[];
  debugCode?: string;
};

export type EmailBootstrapCompletePayload = {
  email: string;
  code: string;
};

export type EmailChangeCompletePayload = {
  email: string;
  currentVerificationMethod?: SecurityVerificationMethod;
  currentCode: string;
  newCode: string;
};

export type DeleteAccountPayload = Omit<DeleteAccountRequest, "verificationMethod"> & {
  verificationMethod: SecurityVerificationMethod;
};

export type LogoutData = LogoutResponse;

export type ActiveSessionDTO = ActiveSessionResponse;

export type ActiveSessionListData = Omit<ActiveSessionListResponse, "results"> & {
  results: ActiveSessionDTO[];
};

export type UpdateCurrentSessionLocationPayload = UpdateCurrentSessionLocationRequest;
