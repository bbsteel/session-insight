// Shared session-ID copy logic: the CLI-resumable identity is resume_id when
// the adapter records one, else the session id (grok/claude set them equal;
// codex resume_id differs from the file-stem id). Used by the session list
// (Sidebar) and the collaboration dock's copy-agent-ID action so both copy
// exactly the same string.

export interface CopyableSessionRef {
  id: string
  resume_id?: string | null
}

export function sessionCopyId(session: CopyableSessionRef): string {
  return session.resume_id || session.id
}

export async function copySessionIdToClipboard(session: CopyableSessionRef): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(sessionCopyId(session))
    return true
  } catch {
    return false
  }
}
