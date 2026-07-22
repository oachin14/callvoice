"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import type { Invitation } from "sip.js";
import {
  api,
  claimNextLead,
  joinAgentCampaign,
  listAgentCampaigns,
  listAgentDispositions,
  parseApiMessage,
  postAgentDisposition,
  type Campaign,
  type Disposition,
  type Lead,
  type User,
} from "../../lib/api";
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

  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [selectedCampaignId, setSelectedCampaignId] = useState("");
  const [joinedCampaignId, setJoinedCampaignId] = useState<string | null>(null);
  const [lead, setLead] = useState<Lead | null>(null);
  const [dispositions, setDispositions] = useState<Disposition[]>([]);
  const [wrapUp, setWrapUp] = useState(false);
  const [ok, setOk] = useState("");

  const softphoneRef = useRef<Softphone | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const userIdRef = useRef<string | null>(null);
  const expectOutboundInvite = useRef(false);
  const outboundInviteTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const presenceRef = useRef<"available" | "paused">("available");
  const callStartedAtRef = useRef<string | null>(null);
  const leadRef = useRef<Lead | null>(null);
  const joinedCampaignRef = useRef<string | null>(null);
  const callUUIDRef = useRef<string | null>(null);

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

  useEffect(() => {
    leadRef.current = lead;
  }, [lead]);

  useEffect(() => {
    joinedCampaignRef.current = joinedCampaignId;
  }, [joinedCampaignId]);

  useEffect(() => {
    callUUIDRef.current = callUUID;
  }, [callUUID]);

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
              if (leadRef.current && joinedCampaignRef.current) {
                setWrapUp(true);
              }
              callStartedAtRef.current = null;
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
        if (me.role === "agent") {
          try {
            const list = await listAgentCampaigns();
            setCampaigns(list);
            setSelectedCampaignId((cur) => cur || (list[0]?.id ?? ""));
          } catch {
            /* campaigns optional until assigned */
          }
        }
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
        if (leadRef.current && joinedCampaignRef.current) {
          setWrapUp(true);
        }
        callStartedAtRef.current = null;
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

  async function onJoinCampaign() {
    if (!selectedCampaignId) return;
    setError("");
    setOk("");
    setBusy(true);
    try {
      await joinAgentCampaign(selectedCampaignId);
      setJoinedCampaignId(selectedCampaignId);
      joinedCampaignRef.current = selectedCampaignId;
      const dispos = await listAgentDispositions(selectedCampaignId);
      setDispositions(dispos);
      setLead(null);
      setWrapUp(false);
      setOk("Campagne rejointe.");
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onNextLead() {
    if (!joinedCampaignId) return;
    setError("");
    setOk("");
    setBusy(true);
    try {
      const next = await claimNextLead(joinedCampaignId);
      if (!next) {
        setLead(null);
        setOk("Plus de leads disponibles.");
        return;
      }
      setLead(next);
      setDialTo(next.phone);
      setWrapUp(false);
      setOk("");
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
      const payload: {
        to: string;
        campaign_id?: string;
        lead_id?: string;
      } = { to: dialTo.trim() };
      if (joinedCampaignId && lead) {
        payload.campaign_id = joinedCampaignId;
        payload.lead_id = lead.id;
      }
      callStartedAtRef.current = new Date().toISOString();
      const res = await edgeApi<{ status: string; call_uuid: string }>(
        "/calls/outbound",
        {
          method: "POST",
          body: JSON.stringify(payload),
        },
      );
      setCallUUID(res.call_uuid);
      scheduleOutboundInviteTimeout(res.call_uuid);
    } catch (err) {
      clearExpectOutboundInvite();
      callStartedAtRef.current = null;
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
      if (lead && joinedCampaignId) {
        setWrapUp(true);
      }
      callStartedAtRef.current = null;
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onDispose(dispositionId: string) {
    if (!lead || !joinedCampaignId) return;
    setError("");
    setOk("");
    setBusy(true);
    try {
      const endedAt = new Date().toISOString();
      const startedAt = callStartedAtRef.current || endedAt;
      let durationSec: number | undefined;
      const startMs = Date.parse(startedAt);
      const endMs = Date.parse(endedAt);
      if (!Number.isNaN(startMs) && !Number.isNaN(endMs) && endMs >= startMs) {
        durationSec = Math.round((endMs - startMs) / 1000);
      }
      await postAgentDisposition({
        campaign_id: joinedCampaignId,
        lead_id: lead.id,
        disposition_id: dispositionId,
        call_uuid: callUUIDRef.current || undefined,
        to_number: lead.phone,
        started_at: startedAt,
        ended_at: endedAt,
        duration_sec: durationSec,
      });
      setOk("Qualification enregistrée.");
      setLead(null);
      setWrapUp(false);
      setDialTo("");
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
          {joinedCampaignId
            ? ` · campagne ${campaigns.find((c) => c.id === joinedCampaignId)?.name || joinedCampaignId.slice(0, 8)}`
            : ""}
        </p>
        {error ? <p className={styles.error}>{error}</p> : null}
        {ok ? <p className={styles.ok}>{ok}</p> : null}

        <audio ref={audioRef} autoPlay playsInline />

        {user?.role === "agent" || campaigns.length > 0 ? (
          <div className={styles.campaignBox}>
            <label className={styles.dialLabel} htmlFor="campaign-select">
              Campagne
            </label>
            <div className={styles.dialRow}>
              <select
                id="campaign-select"
                className={styles.dialInput}
                value={selectedCampaignId}
                onChange={(e) => setSelectedCampaignId(e.target.value)}
                disabled={busy || inCall || wrapUp}
              >
                <option value="">Sélectionner…</option>
                {campaigns.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
              <button
                type="button"
                disabled={busy || !selectedCampaignId || inCall}
                onClick={onJoinCampaign}
              >
                Rejoindre
              </button>
            </div>
          </div>
        ) : null}

        {joinedCampaignId ? (
          <div className={styles.leadBox}>
            <div className={styles.actions}>
              <button
                type="button"
                disabled={busy || inCall || wrapUp || !registered}
                onClick={onNextLead}
              >
                Prochain lead
              </button>
            </div>
            {lead ? (
              <div className={styles.leadCard}>
                <p className={styles.leadPhone}>{lead.phone}</p>
                <p className={styles.softphoneSub}>Lead {lead.id.slice(0, 8)}…</p>
                {Object.keys(lead.payload || {}).length > 0 ? (
                  <dl className={styles.leadPayload}>
                    {Object.entries(lead.payload).map(([k, v]) => (
                      <div key={k}>
                        <dt>{k}</dt>
                        <dd>{v}</dd>
                      </div>
                    ))}
                  </dl>
                ) : null}
              </div>
            ) : (
              <p className={styles.softphoneSub}>Aucun lead en cours.</p>
            )}
          </div>
        ) : null}

        {wrapUp && lead && dispositions.length > 0 ? (
          <div className={styles.wrapUp}>
            <p className={styles.softphoneTitle}>Qualification</p>
            <p className={styles.softphoneSub}>
              Choisissez une disposition pour {lead.phone}
            </p>
            <div className={styles.dispoRow}>
              {dispositions.map((d) => (
                <button
                  key={d.id}
                  type="button"
                  disabled={busy}
                  onClick={() => void onDispose(d.id)}
                >
                  {d.label}
                </button>
              ))}
            </div>
          </div>
        ) : null}

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
                disabled={busy || inCall || pendingOutbound || !!ringing || wrapUp}
              />
              {inCall || pendingOutbound ? (
                <button type="button" disabled={busy} onClick={onHangupMedia}>
                  Raccrocher
                </button>
              ) : (
                <button
                  type="button"
                  disabled={busy || !dialTo.trim() || !!ringing || wrapUp}
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
