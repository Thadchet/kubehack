// The API remembers the ports of every forward that started, newest first.
export interface RecentForward {
  port: number;
  localPort: number;
}

// The port to offer when the dialog opens: the one forwarded last, or else the
// first one the object declares.
export function initialPort(recent: RecentForward[], declared: number[]): string {
  const port = recent[0]?.port ?? declared[0];
  return port === undefined ? "" : String(port);
}

// The local port used last time for this object and port, if any.
export function rememberedLocalPort(recent: RecentForward[], port: string): string {
  const match = recent.find((forward) => String(forward.port) === port.trim());
  return match ? String(match.localPort) : "";
}
