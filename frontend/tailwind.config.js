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
        'agent':   ['8px',  { lineHeight: '1.2', fontWeight: '500', letterSpacing: '0.02em' }],
        'section': ['9px',  { lineHeight: '1.2', fontWeight: '600', letterSpacing: '0.04em' }],
        'meta':    ['10px', { lineHeight: '1.3' }],
        'helper':  ['11px', { lineHeight: '1.4' }],
        'nav':     ['12px', { lineHeight: '1.5', fontWeight: '450' }],
        'body':    ['13px', { lineHeight: '1.5' }],
        'prose':   ['14px', { lineHeight: '1.7' }],
        'card':    ['20px', { lineHeight: '1.2', fontWeight: '600' }],
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
        sans: ["'Inter'", '-apple-system', 'BlinkMacSystemFont', "'Segoe UI'", "'PingFang SC'", "'Microsoft YaHei'", 'sans-serif'],
        mono: ["'JetBrains Mono'", "'SF Mono'", "'Fira Code'", "'Menlo'", "'Consolas'", 'monospace'],
      },
    },
  },
}
