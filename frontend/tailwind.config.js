export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        surface: {
          primary: 'var(--bg-primary)',
          DEFAULT: 'var(--bg-surface)',
          hover: 'var(--bg-surface-hover)',
          inset: 'var(--bg-inset)',
        },
        border: {
          DEFAULT: 'var(--border-default)',
          muted: 'var(--border-muted)',
        },
        text: {
          primary: 'var(--text-primary)',
          secondary: 'var(--text-secondary)',
          muted: 'var(--text-muted)',
        },
        accent: {
          blue: 'var(--accent-blue)',
          rose: 'var(--accent-rose)',
          purple: 'var(--accent-purple)',
          amber: 'var(--accent-amber)',
          green: 'var(--accent-green)',
          coral: 'var(--accent-coral)',
          red: 'var(--accent-red)',
          indigo: 'var(--accent-indigo)',
          teal: 'var(--accent-teal)',
          cyan: 'var(--accent-cyan)',
          pink: 'var(--accent-pink)',
          lime: 'var(--accent-lime)',
          orange: 'var(--accent-orange)',
          sky: 'var(--accent-sky)',
        },
      },
      fontSize: {
        'agent':   ['var(--text-agent-size)',   { lineHeight: '1.2', fontWeight: '500', letterSpacing: '0.02em' }],
        'section': ['var(--text-section-size)', { lineHeight: '1.2', fontWeight: '600', letterSpacing: '0.04em' }],
        'meta':    ['var(--text-meta-size)',    { lineHeight: '1.3' }],
        'helper':  ['var(--text-helper-size)',  { lineHeight: '1.4' }],
        'nav':     ['var(--text-nav-size)',     { lineHeight: '1.5', fontWeight: '450' }],
        'body':    ['var(--text-body-size)',    { lineHeight: '1.5' }],
        'prose':   ['var(--text-prose-size)',   { lineHeight: '1.7' }],
        'card':    ['var(--text-card-size)',    { lineHeight: '1.2', fontWeight: '600' }],
      },
      borderRadius: {
        'sm': '4px',
        'md': '6px',
        'lg': '8px',
      },
      transitionDuration: {
        'fast': '100ms',
        'normal': '120ms',
        'slow': '150ms',
      },
      fontFamily: {
        sans: ['var(--font-sans-first)', "'Inter'", 'system-ui', '-apple-system', 'BlinkMacSystemFont', "'Segoe UI'", "'Microsoft YaHei UI'", "'Microsoft YaHei'", "'PingFang SC'", 'sans-serif'],
        mono: ['var(--font-mono-first)', "'JetBrains Mono'", "'Consolas'", "'Menlo'", "'SF Mono'", "'Fira Code'", 'monospace'],
      },
    },
  },
}
