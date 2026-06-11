export default function ReplayView() {
  return (
    <main className="flex-1 flex flex-col min-w-[360px] overflow-hidden">
      {/* Top bar (40px) */}
      <header
        className="flex-shrink-0 border-b border-[var(--border-default)] bg-[var(--bg-surface)] flex items-center px-4"
        style={{ height: '40px' }}
      >
        <span className="text-nav font-semibold text-[var(--text-primary)]">SessionInsight</span>
      </header>

      {/* Empty state */}
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center">
          <div className="text-4xl mb-4 opacity-40">&#128269;</div>
          <h3 className="text-body font-medium text-[var(--text-primary)]">
            Select a session
          </h3>
          <p className="text-helper text-[var(--text-muted)] mt-1">
            Choose a session from the sidebar to view its replay.
          </p>
        </div>
      </div>
    </main>
  )
}
