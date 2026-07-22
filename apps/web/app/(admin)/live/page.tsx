"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, parseApiMessage, User } from "@/lib/api";
import {
  connectLiveWallboard,
  Wallboard,
  WallboardAgent,
  WallboardCall,
} from "@/lib/live";
import styles from "../admin.module.css";

const empty: Wallboard = {
  counts: { available: 0, paused: 0, on_call: 0, calls: 0 },
  agents: [],
  calls: [],
};

export default function LivePage() {
  const router = useRouter();
  const [wallboard, setWallboard] = useState<Wallboard>(empty);
  const [error, setError] = useState("");
  const [connected, setConnected] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let disconnect: (() => void) | null = null;
    let cancelled = false;

    async function start() {
      try {
        const me = await api<User>("/auth/me");
        if (me.role !== "admin" && me.role !== "supervisor") {
          router.replace(me.role === "agent" ? "/agent" : "/login");
          return;
        }
        if (cancelled) return;
        setReady(true);
        disconnect = connectLiveWallboard({
          onSnapshot: (wb) => {
            setWallboard({
              counts: wb.counts ?? empty.counts,
              agents: wb.agents ?? [],
              calls: wb.calls ?? [],
            });
            setConnected(true);
            setError("");
          },
          onError: () => {
            setError("Connexion live interrompue — reconnexion…");
            setConnected(false);
          },
          onClose: () => setConnected(false),
        });
      } catch (err) {
        if (
          err instanceof ApiError &&
          (err.status === 401 || err.status === 403)
        ) {
          router.replace("/login");
          return;
        }
        setError(parseApiMessage(err));
      }
    }

    void start();
    return () => {
      cancelled = true;
      disconnect?.();
    };
  }, [router]);

  if (!ready && !error) {
    return <p className={styles.muted}>Chargement…</p>;
  }

  const { counts, agents, calls } = wallboard;

  return (
    <>
      <header className={styles.pageHeader}>
        <h1 className={styles.title}>Live</h1>
        <p className={styles.muted}>
          Wallboard temps réel ·{" "}
          {connected ? "connecté" : "hors ligne / reconnexion…"}
        </p>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Compteurs</h2>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Disponibles</th>
                <th>Pause</th>
                <th>En appel</th>
                <th>Appels actifs</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>{counts.available}</td>
                <td>{counts.paused}</td>
                <td>{counts.on_call}</td>
                <td>{counts.calls}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Agents</h2>
        <AgentsTable agents={agents} />
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Appels</h2>
        <CallsTable calls={calls} />
      </section>
    </>
  );
}

function AgentsTable({ agents }: { agents: WallboardAgent[] }) {
  return (
    <div className={styles.tableWrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>Agent</th>
            <th>État</th>
            <th>Campagne</th>
          </tr>
        </thead>
        <tbody>
          {agents.length === 0 ? (
            <tr>
              <td colSpan={3} className={styles.muted}>
                Aucun agent connecté.
              </td>
            </tr>
          ) : (
            agents.map((a) => (
              <tr key={a.user_id}>
                <td>{a.user_id}</td>
                <td>{a.state}</td>
                <td>{a.campaign_id || "—"}</td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}

function CallsTable({ calls }: { calls: WallboardCall[] }) {
  return (
    <div className={styles.tableWrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>UUID</th>
            <th>Agent</th>
            <th>Vers</th>
            <th>Campagne</th>
            <th>Début</th>
          </tr>
        </thead>
        <tbody>
          {calls.length === 0 ? (
            <tr>
              <td colSpan={5} className={styles.muted}>
                Aucun appel actif.
              </td>
            </tr>
          ) : (
            calls.map((c) => (
              <tr key={c.uuid}>
                <td>{c.uuid.slice(0, 8)}…</td>
                <td>{c.agent_id}</td>
                <td>{c.to}</td>
                <td>{c.campaign_id || "—"}</td>
                <td>
                  {c.started_at
                    ? new Date(c.started_at).toLocaleString("fr-FR")
                    : "—"}
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
