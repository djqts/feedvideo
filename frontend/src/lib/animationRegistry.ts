const playedAnimations = new Set<string>()

export function shouldAnimate(key: string): boolean {
  if (playedAnimations.has(key)) return false
  playedAnimations.add(key)
  return true
}

export function resetRegistry(): void {
  playedAnimations.clear()
}
