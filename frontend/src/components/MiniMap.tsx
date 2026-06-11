export default function MiniMap() {
  return (
    <nav
      className="h-full flex-shrink-0 border-r border-[var(--border-default)] bg-[var(--bg-inset)]"
      style={{ width: '64px' }}
    >
      <div className="flex items-center justify-center h-full">
        <span className="text-meta text-[var(--text-muted)]" style={{ writingMode: 'vertical-rl' }}>
          MiniMap
        </span>
      </div>
    </nav>
  )
}
