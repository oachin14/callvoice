const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  status: number;
  body: string;

  constructor(status: number, body: string) {
    super(body || `HTTP ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

export function parseApiMessage(err: unknown): string {
  if (err instanceof ApiError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: string };
      switch (parsed.error) {
        case "invalid_credentials":
          return "Identifiants incorrects.";
        case "account_locked":
          return "Compte temporairement verrouillé. Réessayez plus tard.";
        case "invalid_totp":
          return "Code d'authentification invalide.";
        case "pending_required":
        case "pending_invalid":
          return "Session 2FA expirée. Reconnectez-vous.";
        case "totp_setup_required":
          return "La double authentification doit être activée.";
        case "unauthorized":
          return "Session expirée. Reconnectez-vous.";
        case "forbidden":
          return "Accès refusé.";
        case "internal_error":
          return "Erreur serveur. Réessayez plus tard.";
        default:
          return "Une erreur est survenue.";
      }
    } catch {
      return err.message || "Une erreur est survenue.";
    }
  }
  if (err instanceof Error) return err.message;
  return "Une erreur est survenue.";
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
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

export type User = {
  id: string;
  email: string;
  role: "admin" | "supervisor" | "agent";
  totp_enabled: boolean;
  created_at: string;
};

export type LoginResponse =
  | { status: "ok"; user: User }
  | { status: "totp_required" }
  | { status: "totp_setup_required"; user: User };

export type Carrier = {
  id: string;
  name: string;
  host: string;
  port: number;
  transport: string;
  username?: string | null;
  password_set: boolean;
  realm?: string | null;
  codecs: string[];
  caller_ids: string[];
  max_cps: number;
  max_channels: number;
  enabled: boolean;
  priority: number;
  created_at: string;
};

export type CreateCarrierInput = {
  name: string;
  host: string;
  port?: number;
  transport?: string;
  username?: string;
  password?: string;
  realm?: string;
  codecs?: string[];
  caller_ids?: string[];
  max_cps?: number;
  max_channels?: number;
  enabled?: boolean;
  priority?: number;
};
