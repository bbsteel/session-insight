type RuntimeTranslator = (key: string, vars?: Record<string, string | number>) => string

let runtimeTranslator: RuntimeTranslator = key => key

export function setRuntimeTranslator(translator: RuntimeTranslator) {
  runtimeTranslator = translator
}

export function localize(key: string, vars?: Record<string, string | number>): string {
  return runtimeTranslator(key, vars)
}
