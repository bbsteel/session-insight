import { useCallback, useEffect, useState } from 'react'
import { useI18n } from '../i18n'
import {
  activateChangeHostProfile,
  listChangeHostProfiles,
  probeChangeHostProfile,
  revokeChangeHostProfile,
} from '../api'
import type { ChangeHostProfileDTO, ChangeHostProfileLifecycle } from '../changePlatformTypes'
import ChangePlatformWizard from './ChangePlatformWizard'

const sectionBox = 'rounded-lg border border-[var(--border-default)] bg-[var(--bg-surface)] p-3.5'
const actionBtn =
  'rounded-md border border-[var(--border-default)] px-2.5 py-1 text-helper text-[var(--text-secondary)] transition-colors duration-fast hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)] disabled:opacity-50 disabled:pointer-events-none'

const LIFECYCLE_ORDER: ChangeHostProfileLifecycle[] = ['active', 'verified', 'degraded', 'draft', 'invalid', 'revoked']

function lifecycleBadgeClass(lifecycle: ChangeHostProfileLifecycle): string {
  switch (lifecycle) {
    case 'active':
      return 'bg-[color-mix(in_srgb,var(--success)_14%,transparent)] text-[var(--success)]'
    case 'verified':
      return 'bg-[color-mix(in_srgb,var(--accent-blue)_12%,transparent)] text-[var(--accent-blue)]'
    case 'degraded':
    case 'invalid':
      return 'bg-[color-mix(in_srgb,var(--warning)_14%,transparent)] text-[var(--warning)]'
    default:
      return 'bg-[var(--bg-surface-hover)] text-[var(--text-muted)]'
  }
}

function capabilityLabel(t: (key: string) => string, id: string, supported: boolean) {
  const state = supported ? 'supported' : 'unsupported'
  return `${t(`changeHosts.capability.${id}`)}: ${t(`changeHosts.supported.${state}`)}`
}

/**
 * Settings tab for OpenAPI change-host profiles: list, lifecycle actions, and
 * the entry to the import wizard. All capability data comes from the backend
 * profile declaration — no platform-name matrix exists here.
 */
export default function SettingsChangeHostsTab() {
  const { t } = useI18n()
  const [profiles, setProfiles] = useState<ChangeHostProfileDTO[] | null>(null)
  const [error, setError] = useState('')
  const [wizardOpen, setWizardOpen] = useState(false)
  const [busy, setBusy] = useState<string>('')

  const reload = useCallback(async () => {
    try {
      setProfiles(await listChangeHostProfiles())
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const run = async (profileId: string, action: () => Promise<unknown>) => {
    setBusy(profileId)
    try {
      await action()
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy('')
    }
  }

  const sorted = [...(profiles ?? [])].sort((a, b) => {
    const order = LIFECYCLE_ORDER.indexOf(a.lifecycle) - LIFECYCLE_ORDER.indexOf(b.lifecycle)
    return order !== 0 ? order : a.display_name.localeCompare(b.display_name)
  })

  return (
    <div className="space-y-4">
      <div className={sectionBox}>
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-body font-medium text-[var(--text-primary)]">{t('changeHosts.title')}</div>
            <div className="mt-0.5 text-helper text-[var(--text-muted)]">{t('changeHosts.help')}</div>
          </div>
          <button
            onClick={() => setWizardOpen(true)}
            className="rounded-md bg-[var(--accent-blue)] px-3 py-1.5 text-helper text-white transition-colors duration-fast hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
          >
            {t('changeHosts.addPlatform')}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-[var(--border-muted)] bg-[var(--bg-surface)] px-3 py-2 text-helper text-[var(--warning)]" role="alert">
          {error}
        </div>
      )}

      {profiles === null && !error && <div className="text-helper text-[var(--text-muted)]">{t('common.loading')}</div>}

      {profiles !== null && sorted.length === 0 && !error && (
        <div className="text-helper text-[var(--text-muted)]">{t('changeHosts.empty')}</div>
      )}

      {sorted.map(profile => (
        <div key={profile.profile_id} className={sectionBox} data-testid="change-host-profile" data-lifecycle={profile.lifecycle}>
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="truncate text-body font-medium text-[var(--text-primary)]">{profile.display_name}</span>
                <span className={`rounded-full px-2 py-0.5 text-meta ${lifecycleBadgeClass(profile.lifecycle)}`}>
                  {t(`changeHosts.lifecycle.${profile.lifecycle}`)}
                </span>
              </div>
              <div className="mt-0.5 truncate font-mono text-helper text-[var(--text-muted)]">{profile.host_id}</div>
              <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-0.5 text-meta text-[var(--text-muted)]">
                <span>{capabilityLabel(t, 'metadata', profile.capabilities.metadata === 'supported')}</span>
                <span>{capabilityLabel(t, 'file_set', profile.capabilities.file_set === 'supported')}</span>
                <span>{capabilityLabel(t, 'patches', profile.capabilities.patches === 'supported')}</span>
                <span>{capabilityLabel(t, 'modes', profile.capabilities.modes === 'supported')}</span>
                <span>{capabilityLabel(t, 'commits', profile.capabilities.commits === 'supported')}</span>
              </div>
              {profile.lifecycle === 'degraded' && profile.last_failure_code && (
                <div className="mt-1 text-helper text-[var(--warning)]">
                  {t(`changeHosts.failure.${profile.last_failure_code}`)}
                </div>
              )}
              {(profile.required_confirmations?.length ?? 0) > 0 && profile.lifecycle === 'draft' && (
                <div className="mt-1 text-helper text-[var(--text-muted)]">
                  {t('changeHosts.pendingConfirmations', { count: profile.required_confirmations!.length })}
                </div>
              )}
            </div>
            <div className="flex flex-shrink-0 flex-col items-end gap-1.5">
              {profile.lifecycle === 'verified' && (
                <button className={actionBtn} disabled={busy === profile.profile_id} onClick={() => run(profile.profile_id, () => activateChangeHostProfile(profile.profile_id))}>
                  {t('changeHosts.action.activate')}
                </button>
              )}
              {(profile.lifecycle === 'draft' || profile.lifecycle === 'invalid' || profile.lifecycle === 'degraded') && (
                <button className={actionBtn} disabled={busy === profile.profile_id} onClick={() => run(profile.profile_id, () => probeChangeHostProfile(profile.profile_id))}>
                  {t('changeHosts.action.reprobe')}
                </button>
              )}
              {profile.lifecycle !== 'revoked' && (
                <button className={actionBtn} disabled={busy === profile.profile_id} onClick={() => run(profile.profile_id, () => revokeChangeHostProfile(profile.profile_id))}>
                  {t('changeHosts.action.revoke')}
                </button>
              )}
            </div>
          </div>
        </div>
      ))}

      {wizardOpen && (
        <ChangePlatformWizard
          onClose={() => setWizardOpen(false)}
          onDone={() => {
            setWizardOpen(false)
            reload()
          }}
        />
      )}
    </div>
  )
}
