type RuntimeTranslator = (key: string, vars?: Record<string, string | number>) => string

const fallbackTranslator: RuntimeTranslator = key => key
let runtimeRegistration: { owner: symbol; translate: RuntimeTranslator } | null = null

// API helpers live outside React and need the locale from the committed app
// tree. Each registration owns its cleanup so an older provider cannot clear
// a newer provider's translator during concurrent effect teardown.
export function registerRuntimeTranslator(translator: RuntimeTranslator): () => void {
  const owner = Symbol('runtime-translator')
  runtimeRegistration = { owner, translate: translator }
  return () => {
    if (runtimeRegistration?.owner === owner) runtimeRegistration = null
  }
}

export function localize(key: string, vars?: Record<string, string | number>): string {
  return (runtimeRegistration?.translate ?? fallbackTranslator)(key, vars)
}
