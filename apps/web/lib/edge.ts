import { ApiError } from "./api";

const EDGE_BASE = process.env.NEXT_PUBLIC_EDGE_URL ?? "http://localhost:8081";

export type WebRTCConfig = {
  wssUrl: string;
  sipUri: string;
  password: string;
  iceServers: { urls: string[] }[];
};

export async function edgeApi<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${EDGE_BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers || {}),
    },
  });

  if (!res.ok) {
    throw new ApiError(res.status, await res.text());
  }

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  if (!text) {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}
