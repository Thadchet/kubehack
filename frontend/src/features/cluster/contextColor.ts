// Every context keeps one hue, so the top band shows which cluster is open
// before any text is read. Lightness and chroma are fixed to keep the band
// legible with dark text in both themes.
const hues = [25, 70, 110, 150, 195, 240, 285, 335];

export function contextColor(name: string): string {
  let hash = 0;
  for (const character of name) {
    hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  }
  return `oklch(0.8 0.11 ${hues[hash % hues.length]})`;
}
