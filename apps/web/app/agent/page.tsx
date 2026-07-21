"use client";

import { useState } from "react";
import Link from "next/link";
import { api, parseApiMessage, type User } from "../../lib/api";
import { edgeApi, type WebRTCConfig } from "../../lib/edge";
import styles from "./agent.module.css";

type PresenceState = "offline" | "available" | "paused";

export default function AgentConsolePage() {
  const [user, setUser] = useState<User | null>(null);
  const [presence, setPresence] = useState<PresenceState>("offline");
  const [webrtc, setWebrtc] = useState<WebRTCConfig | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialTo, setDialTo] = useState("");
  const [callUUID, setCallUUID] = useState<string | null>(null);
  const [inCall, setInCall] = useState(false);

  async function ensureUser() {
    if (user) return user;
    const me = await api<User>("/auth/me");
    setUser(me);
    return me;
  }

  async function onConnect() {
    setError("");
    setBusy(true);
    try {
      await ensureUser();
      const res = await edgeApi<{ status: string; state: string; webrtc: WebRTCConfig }>(
        "/agent/session/start",
        { method: "POST" },
      );
      setPresence("available");
      setWebrtc(res.webrtc);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onDisconnect() {
    setError("");
    setBusy(true);
    try {
      if (inCall) {
        try {
          await edgeApi("/calls/hangup", { method: "POST", body: JSON.stringify({}) });
        } catch {
          /* best-effort */
        }
      }
      await edgeApi("/agent/session/stop", { method: "POST" });
      setPresence("offline");
      setWebrtc(null);
      setInCall(false);
      setCallUUID(null);
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
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function refreshConfig() {
    setError("");
    setBusy(true);
    try {
      const cfg = await edgeApi<WebRTCConfig>("/agent/webrtc-config");
      setWebrtc(cfg);
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
      const res = await edgeApi<{ status: string; call_uuid: string }>(
        "/calls/outbound",
        {
          method: "POST",
          body: JSON.stringify({ to: dialTo.trim() }),
        },
      );
      setCallUUID(res.call_uuid);
      setInCall(true);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onHangup() {
    setError("");
    setBusy(true);
    try {
      await edgeApi("/calls/hangup", {
        method: "POST",
        body: JSON.stringify(callUUID ? { uuid: callUUID } : {}),
      });
      setInCall(false);
      setCallUUID(null);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <p className={styles.brand}>CallVoice</p>
        <h1 className={styles.title}>Console agent</h1>
        <p className={styles.sub}>
          Session WebRTC + appel sortant manuel (originate serveur).
        </p>
        <Link className={styles.link} href="/login">
          Connexion
        </Link>
      </header>

      <section className={styles.panel}>
        <p className={styles.status}>
          État : <strong>{presence}</strong>
          {inCall ? " · en appel" : ""}
          {user ? ` · ${user.email}` : ""}
        </p>
        {error ? <p className={styles.error}>{error}</p> : null}

        <div className={styles.actions}>
          {presence === "offline" ? (
            <button type="button" disabled={busy} onClick={onConnect}>
              Se connecter
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
                <button type="button" disabled={busy || inCall} onClick={() => setState("available")}>
                  Disponible
                </button>
              )}
              <button type="button" disabled={busy} onClick={refreshConfig}>
                Rafraîchir WebRTC
              </button>
            </>
          )}
        </div>

        {presence !== "offline" ? (
          <div className={styles.dial}>
            <label className={styles.dialLabel} htmlFor="dial-to">
              Numéro (E.164)
            </label>
            <div className={styles.dialRow}>
              <input
                id="dial-to"
                type="tel"
                className={styles.dialInput}
                placeholder="+33123456789"
                value={dialTo}
                onChange={(e) => setDialTo(e.target.value)}
                disabled={busy || inCall}
              />
              {!inCall ? (
                <button type="button" disabled={busy || !dialTo.trim()} onClick={onCall}>
                  Appeler
                </button>
              ) : (
                <button type="button" disabled={busy} onClick={onHangup}>
                  Raccrocher
                </button>
              )}
            </div>
          </div>
        ) : null}

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
