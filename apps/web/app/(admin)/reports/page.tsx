"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  ApiError,
  Campaign,
  listCampaigns,
  parseApiMessage,
  User,
} from "@/lib/api";
import styles from "../admin.module.css";

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

type ReportSummary = {
  calls: number;
  total_duration_sec: number;
  avg_duration_sec: number;
  by_disposition: { code: string; label: string; count: number }[];
  contact_rate?: number | null;
  success_rate?: number | null;
};

function toRFC3339Local(datetimeLocal: string): string {
  if (!datetimeLocal) return "";
  const d = new Date(datetimeLocal);
  if (Number.isNaN(d.getTime())) return "";
  return d.toISOString();
}

function defaultFrom(): string {
  const d = new Date();
  d.setDate(d.getDate() - 7);
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

function defaultTo(): string {
  const d = new Date();
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

export default function ReportsPage() {
  const router = useRouter();
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [agents, setAgents] = useState<User[]>([]);
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState(defaultTo);
  const [campaignId, setCampaignId] = useState("");
  const [agentId, setAgentId] = useState("");
  const [summary, setSummary] = useState<ReportSummary | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const loadMeta = useCallback(async () => {
    setError("");
    try {
      const me = await api<User>("/auth/me");
      if (me.role !== "admin" && me.role !== "supervisor") {
        router.replace(me.role === "agent" ? "/agent" : "/login");
        return;
      }
      const list = await listCampaigns();
      setCampaigns(list);
      if (me.role === "admin") {
        try {
          const users = await api<User[]>("/admin/users");
          setAgents(users.filter((u) => u.role === "agent"));
        } catch {
          /* optional */
        }
      }
    } catch (err) {
      if (
        err instanceof ApiError &&
        (err.status === 401 || err.status === 403)
      ) {
        router.replace("/login");
        return;
      }
      setError(parseApiMessage(err));
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => {
    void loadMeta();
  }, [loadMeta]);

  function buildQuery(): string {
    const q = new URLSearchParams();
    const fromIso = toRFC3339Local(from);
    const toIso = toRFC3339Local(to);
    if (fromIso) q.set("from", fromIso);
    if (toIso) q.set("to", toIso);
    if (campaignId) q.set("campaign_id", campaignId);
    if (agentId.trim()) q.set("agent_id", agentId.trim());
    const s = q.toString();
    return s ? `?${s}` : "";
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const data = await api<ReportSummary>(
        `/admin/reports/summary${buildQuery()}`,
      );
      setSummary(data);
    } catch (err) {
      setError(parseApiMessage(err));
      setSummary(null);
    } finally {
      setBusy(false);
    }
  }

  async function onDownloadCSV() {
    setError("");
    setBusy(true);
    try {
      const res = await fetch(
        `${API_BASE}/admin/reports/export.csv${buildQuery()}`,
        { credentials: "include" },
      );
      if (!res.ok) {
        throw new ApiError(res.status, await res.text());
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "callvoice-report.csv";
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (loading) {
    return <p className={styles.muted}>Chargement…</p>;
  }

  return (
    <>
      <header className={styles.pageHeader}>
        <h1 className={styles.title}>Rapports</h1>
        <p className={styles.muted}>
          Synthèse et export CSV des appels qualifiés.
        </p>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Filtres</h2>
        <form className={styles.form} onSubmit={onSubmit}>
          <label>
            Du
            <input
              type="datetime-local"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </label>
          <label>
            Au
            <input
              type="datetime-local"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            />
          </label>
          <label>
            Campagne
            <select
              value={campaignId}
              onChange={(e) => setCampaignId(e.target.value)}
            >
              <option value="">Toutes</option>
              {campaigns.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Agent
            {agents.length > 0 ? (
              <select
                value={agentId}
                onChange={(e) => setAgentId(e.target.value)}
              >
                <option value="">Tous</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.display_name || a.email}
                  </option>
                ))}
              </select>
            ) : (
              <input
                value={agentId}
                onChange={(e) => setAgentId(e.target.value)}
                placeholder="UUID agent (optionnel)"
              />
            )}
          </label>
          <button type="submit" className={styles.submit} disabled={busy}>
            {busy ? "Chargement…" : "Afficher"}
          </button>
          <button
            type="button"
            className={styles.ghost}
            disabled={busy}
            onClick={() => void onDownloadCSV()}
          >
            Télécharger CSV
          </button>
        </form>
      </section>

      {summary ? (
        <>
          <section className={styles.section}>
            <h2 className={styles.sectionTitle}>Synthèse</h2>
            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>Appels</th>
                    <th>Durée totale (s)</th>
                    <th>Durée moy. (s)</th>
                    <th>Taux contact</th>
                    <th>Taux succès</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>{summary.calls}</td>
                    <td>{summary.total_duration_sec}</td>
                    <td>{summary.avg_duration_sec.toFixed(1)}</td>
                    <td>
                      {summary.contact_rate != null
                        ? `${(summary.contact_rate * 100).toFixed(1)} %`
                        : "—"}
                    </td>
                    <td>
                      {summary.success_rate != null
                        ? `${(summary.success_rate * 100).toFixed(1)} %`
                        : "—"}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <section className={styles.section}>
            <h2 className={styles.sectionTitle}>Par disposition</h2>
            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>Code</th>
                    <th>Libellé</th>
                    <th>Count</th>
                  </tr>
                </thead>
                <tbody>
                  {summary.by_disposition.length === 0 ? (
                    <tr>
                      <td colSpan={3} className={styles.muted}>
                        Aucune donnée.
                      </td>
                    </tr>
                  ) : (
                    summary.by_disposition.map((d) => (
                      <tr key={d.code}>
                        <td>{d.code}</td>
                        <td>{d.label}</td>
                        <td>{d.count}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </section>
        </>
      ) : null}
    </>
  );
}
