import { Connection as loadDesktopConnection } from "../wailsjs/go/main/App";

export interface Connection {
  baseUrl: string;
  token: string;
}

let connection: Promise<Connection> | undefined;

function loadConnection(): Promise<Connection> {
  if ("go" in window) {
    return loadDesktopConnection();
  }
  return Promise.resolve({
    baseUrl: import.meta.env.VITE_KUBEHACK_API,
    token: import.meta.env.VITE_KUBEHACK_TOKEN,
  });
}

export async function apiGet<T>(path: string): Promise<T> {
  connection ??= loadConnection();
  const { baseUrl, token } = await connection;
  const response = await fetch(`${baseUrl}${path}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!response.ok) {
    throw new Error(`${response.status}: ${await response.text()}`);
  }
  return response.json();
}

// Browsers cannot set headers on a WebSocket, so the token travels as a
// subprotocol. The server selects "kubehack.v1" and never echoes the token.
export async function openSocket(path: string): Promise<WebSocket> {
  connection ??= loadConnection();
  const { baseUrl, token } = await connection;
  return new WebSocket(`${baseUrl.replace(/^http/, "ws")}${path}`, [
    "kubehack.v1",
    `kubehack.token.${token}`,
  ]);
}
