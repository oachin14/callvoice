"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import type { Invitation } from "sip.js";
import { api, parseApiMessage, type User } from "../../lib/api";
import { edgeApi, type WebRTCConfig } from "../../lib/edge";
import { Softphone } from "../../lib/softphone";
import { SoftphonePanel } from "./softphone";
import styles from "./agent.module.css";

type AgentUIState = "offline" | "connecting" | "available" | "paused" | "in_call";

const OUTBOUND_INVITE_TIMEOUT_MS = 30_000;

export default function AgentConsolePage() {
  const [user, setUser] = useState<User | null>(null);
  const [uiState, setUiState] = useState<AgentUIState>("offline");
  const [presence, setPresence] = useState<"available" | "paused">("available");
  const [webrtc, setWebrtc] = useState<WebRTCConfig | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialTo, setDialTo] = useState("");
  const [callUUID, setCallUUID] = useState<string | null>(null);
  const [ringing, setRinging] = useState<Invitation | null>(null);
  const [muted, setMuted] = useState(false);
  const [held, setHeld] = useState(false);

  const softphoneRef = useRef<Softphone | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const userIdRef = useRef<string | null>(null);
  const expectOutboundInvite = useRef(false);
  const outboundInviteTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const presenceRef = useRef<"available" | "paused">("available");

  function clearOutboundInviteTimeout() {
    if (outboundInviteTimeoutRef.current !== null) {
      clearTimeout(outboundInviteTimeoutRef.current);
      outboundInviteTimeoutRef.current = null;
    }
  }

  function clearExpectOutboundInvite() {
    expectOutboundInvite.current = false;
    clearOutboundInviteTimeout();
  }

  function scheduleOutboundInviteTimeout(uuid: string) {
    clearOutboundInviteTimeout();
    outboundInviteTimeoutRef.current = setTimeout(() => {
      outboundInviteTimeoutRef.current = null;
      if (!expectOutboundInvite.current) return;
      clearExpectOutboundInvite();
      setError("L'invitation média n'est pas arrivée dans les 30 secondes.");
      void edgeApi("/calls/hangup", {
        method: "POST",
        body: JSON.stringify({ uuid }),
      }).catch(() => {
        /* best-effort */
      });
      setCallUUID(null);
      setUiState(presenceRef.current);
    }, OUTBOUND_INVITE_TIMEOUT_MS);
  }

  useEffect(() => {
    presenceRef.current = presence;
  }, [presence]);

  useEffect(() => () => clearOutboundInviteTimeout(), []);

  useEffect(() => {
    const phone = new Softphone();
    softphoneRef.current = phone;
    return () => {
      void phone.disconnect();
      softphoneRef.current = null;
    };
  }, []);

  useEffect(() => {
    softphoneRef.current?.setRemoteAudio(audioRef.current);
  }, []);

  useEffect(() => {
    userIdRef.current = user?.id ?? null;
  }, [user]);

  // Live statuses from edge GET /ws (cookie auth; wait for API session).
  useEffect(() => {
    const edgeBase = process.env.NEXT_PUBLIC_EDGE_URL ?? "http://localhost:8081";
    const wsURL = `${edgeBase.replace(/^http/, "ws")}/ws`;
    let ws: WebSocket | null = null;
    let closed = false;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;

    function liveEventUserId(payload: Record<string, unknown>): string | null {
      const raw = payload.user_id ?? payload.agent_id;
      return raw != null ? String(raw) : null;
    }

    function isOwnLiveEvent(payload: Record<string, unknown> | undefined): boolean {
      if (!payload) return false;
      const mine = userIdRef.current;
      const from = liveEventUserId(payload);
      if (!from) return true;
      return mine != null && from === mine;
    }

    function openSocket() {
      if (closed) return;
      ws = new WebSocket(wsURL);
      ws.onmessage = (msg) => {
        try {
          const ev = JSON.parse(String(msg.data)) as {
            type?: string;
            payload?: Record<string, unknown>;
          };
          if (ev.type === "agent.state" && ev.payload) {
            if (!isOwnLiveEvent(ev.payload)) return;
            const state = String(ev.payload.state ?? "");
            if (state === "available" || state === "paused") {
              setPresence(state);
              presenceRef.current = state;
              setUiState((cur) =>
                cur === "in_call" || cur === "offline" || cur === "connecting" ? cur : state,
              );
            } else if (state === "offline") {
              setUiState((cur) => (cur === "connecting" ? cur : "offline"));
            }
          }
          if (ev.type === "call.state" && ev.payload) {
            if (!isOwnLiveEvent(ev.payload)) return;
            const state = String(ev.payload.state ?? "");
            const uuid = ev.payload.call_uuid != null ? String(ev.payload.call_uuid) : "";
            if (state === "active" && uuid) {
              setCallUUID(uuid);
            } else if (state === "ended") {
              clearExpectOutboundInvite();
              setCallUUID(null);
              setRinging(null);
              setMuted(false);
              setHeld(false);
              setUiState((cur) => (cur === "in_call" ? presenceRef.current : cur));
            }
          }
        } catch {
          /* ignore malformed */
        }
      };
      ws.onclose = () => {
        if (closed) return;
        retryTimer = setTimeout(() => {
          void ensureSessionThenConnect();
        }, 3000);
      };
    }

    async function ensureSessionThenConnect() {
      if (closed) return;
      try {
        const me = await api<User>("/auth/me");
        userIdRef.current = me.id;
        setUser(me);
      } catch {
        retryTimer = setTimeout(() => {
          void ensureSessionThenConnect();
        }, 5000);
        return;
      }
      openSocket();
    }

    void ensureSessionThenConnect();
    return () => {
      closed = true;
      if (retryTimer) clearTimeout(retryTimer);
      ws?.close();
    };
  }, []);

  function wireSoftphone(phone: Softphone) {
    phone.setRemoteAudio(audioRef.current);
    phone.setCallbacks({
      onInvite: (invitation) => {
        if (expectOutboundInvite.current) {
          clearExpectOutboundInvite();
          void phone
            .answer(invitation)
            .then(() => {
              setRinging(null);
              setMuted(false);
              setHeld(false);
              setUiState("in_call");
            })
            .catch((err) => setError(parseApiMessage(err)));
          return;
        }
        setRinging(invitation);
      },
      onBye: () => {
        clearExpectOutboundInvite();
        setRinging(null);
        setMuted(false);
        setHeld(false);
        setCallUUID(null);
        setUiState(presenceRef.current);
      },
      onError: (err) => setError(err.message),
    });
  }

  async function ensureUser() {
    if (user) return user;
    const me = await api<User>("/auth/me");
    setUser(me);
    return me;
  }

  async function onConnect() {
    setError("");
    setBusy(true);
    setUiState("connecting");
    try {
      await ensureUser();
      const res = await edgeApi<{ status: string; state: string; webrtc: WebRTCConfig }>(
        "/agent/session/start",
        { method: "POST" },
      );
      setWebrtc(res.webrtc);
      setPresence("available");
      presenceRef.current = "available";

      const phone = softphoneRef.current;
      if (!phone) throw new Error("Softphone unavailable");
      wireSoftphone(phone);
      await phone.connect(res.webrtc);
      setUiState("available");
    } catch (err) {
      setError(parseApiMessage(err));
      setUiState("offline");
      setWebrtc(null);
      try {
        await softphoneRef.current?.disconnect();
      } catch {
        /* ignore */
      }
    } finally {
      setBusy(false);
    }
  }

  async function onDisconnect() {
    setError("");
    setBusy(true);
    try {
      if (uiState === "in_call" || callUUID) {
        try {
          await edgeApi("/calls/hangup", {
            method: "POST",
            body: JSON.stringify(callUUID ? { uuid: callUUID } : {}),
          });
        } catch {
          /* best-effort */
        }
      }
      clearExpectOutboundInvite();
      await softphoneRef.current?.disconnect();
      await edgeApi("/agent/session/stop", { method: "POST" });
      setUiState("offline");
      setWebrtc(null);
      setCallUUID(null);
      setRinging(null);
      setMuted(false);
      setHeld(false);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function setState(state: "available" | "paused") {
    setError("");
    setBusy(true);
    try {
      await edgeApi("/agent/state", {
        method: "POST",
        body: JSON.stringify({ state }),
      });
      setPresence(state);
      presenceRef.current = state;
      if (uiState !== "in_call") {
        setUiState(state);
      }
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onCall() {
    setError("");
    setBusy(true);
    try {
      expectOutboundInvite.current = true;
      const res = await edgeApi<{ status: string; call_uuid: string }>(
        "/calls/outbound",
        {
          method: "POST",
          body: JSON.stringify({ to: dialTo.trim() }),
        },
      );
      setCallUUID(res.call_uuid);
      scheduleOutboundInviteTimeout(res.call_uuid);
    } catch (err) {
      clearExpectOutboundInvite();
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onHangupMedia() {
    setError("");
    setBusy(true);
    clearExpectOutboundInvite();
    try {
      await softphoneRef.current?.hangup();
      if (callUUID) {
        try {
          await edgeApi("/calls/hangup", {
            method: "POST",
            body: JSON.stringify({ uuid: callUUID }),
          });
        } catch {
          /* media hangup may already tear down */
        }
      }
      setCallUUID(null);
      setRinging(null);
      setMuted(false);
      setHeld(false);
      setUiState(presenceRef.current);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onAnswer() {
    setError("");
    setBusy(true);
    try {
      const inv = ringing;
      if (!inv) return;
      await softphoneRef.current?.answer(inv);
      setRinging(null);
      setMuted(false);
      setHeld(false);
      setUiState("in_call");
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onReject() {
    setError("");
    setBusy(true);
    try {
      await softphoneRef.current?.hangup();
      setRinging(null);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function onMuteToggle() {
    const phone = softphoneRef.current;
    if (!phone) return;
    const next = !muted;
    phone.mute(next);
    setMuted(next);
  }

  async function onHoldToggle() {
    setError("");
    setBusy(true);
    try {
      const phone = softphoneRef.current;
      if (!phone) return;
      const next = !held;
      await phone.hold(next);
      setHeld(next);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onDtmf(digit: string) {
    try {
      await softphoneRef.current?.sendDTMF(digit);
    } catch (err) {
      setError(parseApiMessage(err));
    }
  }

  const offline = uiState === "offline";
  const connecting = uiState === "connecting";
  const inCall = uiState === "in_call";
  const pendingOutbound = !!callUUID && !inCall;
  const registered = !offline && !connecting;

  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <p className={styles.brand}>CallVoice</p>
        <h1 className={styles.title}>Console agent</h1>
        <p className={styles.sub}>
          Softphone WebRTC (SIP.js) — média navigateur, originate serveur.
        </p>
        <Link className={styles.link} href="/login">
          Connexion
        </Link>
      </header>

      <section className={styles.panel}>
        <p className={styles.status}>
          État : <strong>{uiState}</strong>
          {user ? ` · ${user.email}` : ""}
        </p>
        {error ? <p className={styles.error}>{error}</p> : null}

        <audio ref={audioRef} autoPlay playsInline />

        <div className={styles.actions}>
          {offline || connecting ? (
            <button type="button" disabled={busy || connecting} onClick={onConnect}>
              {connecting ? "Connexion…" : "Se connecter"}
            </button>
          ) : (
            <>
              <button type="button" disabled={busy} onClick={onDisconnect}>
                Se déconnecter
              </button>
              {presence === "available" ? (
                <button type="button" disabled={busy || inCall} onClick={() => setState("paused")}>
                  Pause
                </button>
              ) : (
                <button
                  type="button"
                  disabled={busy || inCall}
                  onClick={() => setState("available")}
                >
                  Disponible
                </button>
              )}
            </>
          )}
        </div>

        {registered ? (
          <div className={styles.dial}>
            <label className={styles.dialLabel} htmlFor="dial-to">
              Numéro (E.164) — originate serveur
            </label>
            <div className={styles.dialRow}>
              <input
                id="dial-to"
                type="tel"
                className={styles.dialInput}
                placeholder="+33123456789"
                value={dialTo}
                onChange={(e) => setDialTo(e.target.value)}
                disabled={busy || inCall || pendingOutbound || !!ringing}
              />
              {inCall || pendingOutbound ? (
                <button type="button" disabled={busy} onClick={onHangupMedia}>
                  Raccrocher
                </button>
              ) : (
                <button
                  type="button"
                  disabled={busy || !dialTo.trim() || !!ringing}
                  onClick={onCall}
                >
                  Appeler
                </button>
              )}
            </div>
          </div>
        ) : null}

        <SoftphonePanel
          ringing={ringing}
          inCall={inCall}
          muted={muted}
          held={held}
          busy={busy}
          onAnswer={onAnswer}
          onReject={onReject}
          onHangup={onHangupMedia}
          onMuteToggle={onMuteToggle}
          onHoldToggle={onHoldToggle}
          onDtmf={onDtmf}
        />

        {webrtc ? (
          <pre className={styles.creds} aria-label="Configuration WebRTC">
            {JSON.stringify(
              {
                wssUrl: webrtc.wssUrl,
                sipUri: webrtc.sipUri,
                password: "(set)",
                iceServers: webrtc.iceServers,
              },
              null,
              2,
            )}
          </pre>
        ) : null}
      </section>
    </main>
  );
}
