import { Connection as loadDesktopConnection } from "../wailsjs/go/main/App";

export interface Connection {
  baseUrl: string;
  token: string;
}

const tokenKey = "kubehack.token";

let connection: Promise<Connection> | undefined;

function loadConnection(): Promise<Connection> {
  if ("go" in window) {
    return loadDesktopConnection();
  }
  if (import.meta.env.VITE_KUBEHACK_API) {
    return Promise.resolve({
      baseUrl: import.meta.env.VITE_KUBEHACK_API,
      token: import.meta.env.VITE_KUBEHACK_TOKEN,
    });
  }
  return Promise.resolve({ baseUrl: location.origin, token: takeUrlToken() });
}

// The Docker version serves this page itself, and opens it with the token in the
// URL fragment, which browsers never send to a server. It moves to session
// storage so that it leaves the address bar but survives a reload.
function takeUrlToken(): string {
  const token = new URLSearchParams(location.hash.slice(1)).get("token");
  if (token) {
    sessionStorage.setItem(tokenKey, token);
    history.replaceState(null, "", location.pathname);
  }
  return sessionStorage.getItem(tokenKey) ?? "";
}

// ApiError keeps the response body, which can say how to recover.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly body: string,
  ) {
    super(`${status}: ${body}`);
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  connection ??= loadConnection();
  const { baseUrl, token } = await connection;
  const response = await fetch(`${baseUrl}${path}`, {
    ...init,
    headers: { ...init.headers, Authorization: `Bearer ${token}` },
  });
  if (!response.ok) {
    throw new ApiError(response.status, await response.text());
  }
  return response.json();
}

export function apiGet<T>(path: string): Promise<T> {
  return request<T>(path);
}

export function apiPost<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export function apiPut<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
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
