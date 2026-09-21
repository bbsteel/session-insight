// Credential-safe DTOs and pure wizard state for OpenAPI change-host profiles.
// The runtime capability declaration from the backend is the single source of
// truth — the frontend never infers capabilities from a platform name.

export type ChangeHostProfileLifecycle =
  | 'draft'
  | 'verified'
  | 'active'
  | 'degraded'
  | 'invalid'
  | 'revoked'

export interface ProfileCapabilities {
  metadata: 'supported' | 'unsupported'
  file_set: 'supported' | 'unsupported'
  patches: 'supported' | 'unsupported'
  modes: 'supported' | 'unsupported'
  commits: 'supported' | 'unsupported'
  content_anchor: string
  repository_id: 'supported' | 'unsupported'
}

export interface FieldCandidateDTO {
  field: string
  pointer: string
  confidence: number
  shape: string
}

export interface RequiredConfirmationDTO {
  role: string
  field: string
  candidates: FieldCandidateDTO[]
  reason: string
}

export interface ChangeHostProfileDTO {
  profile_id: string
  host_id: string
  profile_revision: number
  display_name: string
  lifecycle: ChangeHostProfileLifecycle
  spec_digest: string
  spec_version: string
  capabilities: ProfileCapabilities
  authentication_configured: boolean
  authentication_mode?: 'token_environment' | 'os_keyring'
  required_confirmations?: RequiredConfirmationDTO[]
  warnings?: string[]
  created_at: string
  updated_at: string
  verified_at?: string
  activated_at?: string
  last_failure_code?: string
}

export interface ImportProfileResponse {
  profile: ChangeHostProfileDTO
  endpoint_origins: string[]
  host: {
    key: string
    provider: string
    display_origin: string
    endpoint_origins: string[]
  }
  candidate_count: number
  requires_host_approval: boolean
}

export interface ProbeProfileResponse {
  profile: ChangeHostProfileDTO
  verified: boolean
  required_confirmations: RequiredConfirmationDTO[]
  warnings: string[]
  capabilities: ProfileCapabilities
}

// --- wizard state machine ---------------------------------------------------

export type WizardStep = 'basics' | 'network' | 'analysis' | 'mapping' | 'finish'

export const WIZARD_STEPS: WizardStep[] = ['basics', 'network', 'analysis', 'mapping', 'finish']

export interface WizardBasics {
  displayName: string
  document: string
  apiBaseUrl: string
  sampleChangeUrl: string
}

export interface WizardNetwork {
  credentialEnvName: string
  allowHttp: boolean
  allowPrivateNetwork: boolean
}

export interface WizardState {
  step: WizardStep
  basics: WizardBasics
  network: WizardNetwork
  imported?: ImportProfileResponse
  probe?: ProbeProfileResponse
  selections: Record<string, string>
  error: string
}

export function initialWizardState(): WizardState {
  return {
    step: 'basics',
    basics: { displayName: '', document: '', apiBaseUrl: '', sampleChangeUrl: '' },
    network: { credentialEnvName: '', allowHttp: false, allowPrivateNetwork: false },
    selections: {},
    error: '',
  }
}

// basicsReady gates step 1: every input is present and the document parses
// structurally (JSON or YAML-ish OpenAPI/Swagger marker).
export function basicsReady(basics: WizardBasics): boolean {
  if (!basics.displayName.trim() || !basics.document.trim() || !basics.apiBaseUrl.trim() || !basics.sampleChangeUrl.trim()) {
    return false
  }
  return /"?(openapi|swagger)"?\s*[:=]/.test(basics.document)
}

export function networkReady(network: WizardNetwork): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(network.credentialEnvName)
}

// confirmationKey identifies one required confirmation for the selections map.
export function confirmationKey(role: string, field: string): string {
  return `${role}.${field}`
}

// mappingComplete reports whether every required confirmation has a selection.
export function mappingComplete(confirmations: RequiredConfirmationDTO[], selections: Record<string, string>): boolean {
  return confirmations.every(c => selections[confirmationKey(c.role, c.field)] !== undefined)
}

export function mappingSelections(confirmations: RequiredConfirmationDTO[], selections: Record<string, string>) {
  return confirmations
    .map(c => ({
      role: c.role,
      field: c.field,
      pointer: selections[confirmationKey(c.role, c.field)],
    }))
    .filter(s => s.pointer !== undefined)
}

// capabilityRows renders the summary table in stable order.
export function capabilityRows(capabilities: ProfileCapabilities): { id: string; supported: boolean }[] {
  return [
    { id: 'metadata', supported: capabilities.metadata === 'supported' },
    { id: 'file_set', supported: capabilities.file_set === 'supported' },
    { id: 'patches', supported: capabilities.patches === 'supported' },
    { id: 'modes', supported: capabilities.modes === 'supported' },
    { id: 'commits', supported: capabilities.commits === 'supported' },
  ]
}
