export default function Sidebar() {
  return (
    <aside
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-surface)]"
      style={{ width: '260px' }}
    >
      <div className="p-4">
        <h2 className="text-nav font-semibold text-[var(--text-primary)]">Sessions</h2>
        <p className="text-helper text-[var(--text-muted)] mt-2">No sessions yet</p>
      </div>
    </aside>
  )
}
