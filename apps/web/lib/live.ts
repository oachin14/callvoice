const EDGE_BASE = process.env.NEXT_PUBLIC_EDGE_URL ?? "http://localhost:8081";

export type WallboardCounts = {
  available: number;
  paused: number;
  on_call: number;
  calls: number;
};

export type WallboardAgent = {
  user_id: string;
  state: string;
  campaign_id?: string | null;
};

export type WallboardCall = {
  uuid: string;
  agent_id: string;
  to: string;
  campaign_id?: string | null;
  started_at: string;
};

export type Wallboard = {
  counts: WallboardCounts;
  agents: WallboardAgent[];
  calls: WallboardCall[];
};

export type LiveSnapshotEvent = {
  type: "live.snapshot";
  payload: Wallboard;
};

type LiveHandlers = {
  onSnapshot: (wb: Wallboard) => void;
  onError?: (err: Error) => void;
  onClose?: () => void;
};

/** Connect to edge `GET /ws/live` (cookie session; admin|supervisor). */
export function connectLiveWallboard(handlers: LiveHandlers): () => void {
  const wsURL = `${EDGE_BASE.replace(/^http/, "ws")}/ws/live`;
  let ws: WebSocket | null = null;
  let closed = false;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;

  function open() {
    if (closed) return;
    ws = new WebSocket(wsURL);
    ws.onmessage = (msg) => {
      try {
        const ev = JSON.parse(String(msg.data)) as {
          type?: string;
          payload?: Wallboard;
        };
        if (ev.type === "live.snapshot" && ev.payload) {
          handlers.onSnapshot(ev.payload);
        }
      } catch (err) {
        handlers.onError?.(
          err instanceof Error ? err : new Error("live_parse_error"),
        );
      }
    };
    ws.onerror = () => {
      handlers.onError?.(new Error("live_ws_error"));
    };
    ws.onclose = () => {
      handlers.onClose?.();
      if (closed) return;
      retryTimer = setTimeout(open, 3000);
    };
  }

  open();

  return () => {
    closed = true;
    if (retryTimer) clearTimeout(retryTimer);
    ws?.close();
  };
}
