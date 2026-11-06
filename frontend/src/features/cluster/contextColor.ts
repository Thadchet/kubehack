// Every context keeps one hue, and the whole interface takes it on, so the app
// shows which cluster is open before any text is read. Lightness and chroma are
// fixed to keep the color legible with dark text in both themes.
const hues = [25, 70, 110, 150, 195, 240, 285, 335];

export function contextHue(name: string): number {
  let hash = 0;
  for (const character of name) {
    hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  }
  return hues[hash % hues.length];
}

export function contextColor(name: string): string {
  return `oklch(0.8 0.11 ${contextHue(name)})`;
}
