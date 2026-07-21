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
      await edgeApi("/agent/session/stop", { method: "POST" });
      setPresence("offline");
      setWebrtc(null);
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

  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <p className={styles.brand}>CallVoice</p>
        <h1 className={styles.title}>Console agent</h1>
        <p className={styles.sub}>
          Session WebRTC (stub) — softphone SIP.js arrive au jalon suivant.
        </p>
        <Link className={styles.link} href="/login">
          Connexion
        </Link>
      </header>

      <section className={styles.panel}>
        <p className={styles.status}>
          État : <strong>{presence}</strong>
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
                <button type="button" disabled={busy} onClick={() => setState("paused")}>
                  Pause
                </button>
              ) : (
                <button type="button" disabled={busy} onClick={() => setState("available")}>
                  Disponible
                </button>
              )}
              <button type="button" disabled={busy} onClick={refreshConfig}>
                Rafraîchir WebRTC
              </button>
            </>
          )}
        </div>

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
