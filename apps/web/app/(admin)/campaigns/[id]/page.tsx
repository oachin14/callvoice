"use client";

import Link from "next/link";
import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import {
  ApiError,
  Campaign,
  CampaignStatus,
  Disposition,
  User,
  api,
  assignCampaignAgents,
  createDisposition,
  importCampaignLeads,
  listCampaigns,
  listDispositions,
  listUsers,
  parseApiMessage,
  patchCampaign,
} from "@/lib/api";
import styles from "../../admin.module.css";

const STATUS_LABEL: Record<CampaignStatus, string> = {
  draft: "Brouillon",
  running: "En cours",
  paused: "En pause",
  stopped: "Arrêtée",
};

function nextActions(status: CampaignStatus): CampaignStatus[] {
  switch (status) {
    case "draft":
      return ["running"];
    case "running":
      return ["paused", "stopped"];
    case "paused":
      return ["running", "stopped"];
    case "stopped":
      return [];
    default:
      return [];
  }
}

export default function CampaignDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const router = useRouter();

  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [agents, setAgents] = useState<User[]>([]);
  const [selectedAgentIds, setSelectedAgentIds] = useState<string[]>([]);
  const [dispositions, setDispositions] = useState<Disposition[]>([]);
  const [canManageUsers, setCanManageUsers] = useState(false);
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const [csvFile, setCsvFile] = useState<File | null>(null);
  const [csvName, setCsvName] = useState("Import");
  const [importSummary, setImportSummary] = useState("");

  const [dispoForm, setDispoForm] = useState({
    code: "",
    label: "",
    is_contact: false,
    is_success: false,
  });

  const load = useCallback(async () => {
    setError("");
    try {
      const me = await api<User>("/auth/me");
      if (me.role !== "admin" && me.role !== "supervisor") {
        router.replace(me.role === "agent" ? "/agent" : "/login");
        return;
      }
      const list = await listCampaigns();
      const found = list.find((c) => c.id === id) ?? null;
      if (!found) {
        setError("Campagne introuvable.");
        setCampaign(null);
        return;
      }
      setCampaign(found);

      const dispos = await listDispositions(id);
      setDispositions(dispos);

      if (me.role === "admin") {
        setCanManageUsers(true);
        try {
          const users = await listUsers();
          setAgents(users.filter((u) => u.role === "agent" && !u.disabled));
        } catch {
          setCanManageUsers(false);
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
  }, [id, router]);

  useEffect(() => {
    void load();
  }, [load]);

  const actions = useMemo(
    () => (campaign ? nextActions(campaign.status) : []),
    [campaign],
  );

  async function onStatus(status: CampaignStatus) {
    if (!campaign) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      const updated = await patchCampaign(campaign.id, { status });
      setCampaign(updated);
      setOk(`Statut : ${STATUS_LABEL[updated.status]}`);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function toggleAgent(userId: string) {
    setSelectedAgentIds((prev) =>
      prev.includes(userId)
        ? prev.filter((x) => x !== userId)
        : [...prev, userId],
    );
  }

  async function onSaveAgents(e: FormEvent) {
    e.preventDefault();
    if (!campaign) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      await assignCampaignAgents(campaign.id, selectedAgentIds);
      setOk("Agents enregistrés.");
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onImport(e: FormEvent) {
    e.preventDefault();
    if (!campaign || !csvFile) return;
    setBusy(true);
    setError("");
    setOk("");
    setImportSummary("");
    try {
      const result = await importCampaignLeads(
        campaign.id,
        csvFile,
        csvName.trim() || "Import",
      );
      setImportSummary(
        `Importé : ${result.imported} · Rejeté : ${result.rejected}`,
      );
      setOk("Liste importée.");
      setCsvFile(null);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onCreateDispo(e: FormEvent) {
    e.preventDefault();
    if (!campaign) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      await createDisposition(campaign.id, {
        code: dispoForm.code.trim(),
        label: dispoForm.label.trim(),
        is_contact: dispoForm.is_contact,
        is_success: dispoForm.is_success,
      });
      setDispoForm({
        code: "",
        label: "",
        is_contact: false,
        is_success: false,
      });
      setOk("Disposition ajoutée.");
      const dispos = await listDispositions(campaign.id);
      setDispositions(dispos);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (loading) {
    return <p className={styles.muted}>Chargement…</p>;
  }

  if (!campaign) {
    return (
      <>
        <p className={styles.error}>{error || "Campagne introuvable."}</p>
        <Link className={styles.ghost} href="/campaigns">
          Retour
        </Link>
      </>
    );
  }

  return (
    <>
      <header className={styles.pageHeader}>
        <p className={styles.muted}>
          <Link href="/campaigns">← Campagnes</Link>
        </p>
        <h1 className={styles.title}>{campaign.name}</h1>
        <p className={styles.muted}>
          Statut : {STATUS_LABEL[campaign.status]} · Mode {campaign.dial_mode}
        </p>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}
      {ok ? <p className={styles.ok}>{ok}</p> : null}

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Statut</h2>
        <div className={styles.rowActions}>
          {actions.length === 0 ? (
            <p className={styles.muted}>Aucune transition possible.</p>
          ) : (
            actions.map((s) => (
              <button
                key={s}
                type="button"
                className={styles.ghost}
                disabled={busy}
                onClick={() => void onStatus(s)}
              >
                Passer en « {STATUS_LABEL[s]} »
              </button>
            ))
          )}
        </div>
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Agents assignés</h2>
        {canManageUsers ? (
          <form onSubmit={onSaveAgents}>
            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th />
                    <th>E-mail</th>
                    <th>Nom</th>
                  </tr>
                </thead>
                <tbody>
                  {agents.length === 0 ? (
                    <tr>
                      <td colSpan={3} className={styles.muted}>
                        Aucun agent disponible.
                      </td>
                    </tr>
                  ) : (
                    agents.map((a) => (
                      <tr key={a.id}>
                        <td>
                          <input
                            type="checkbox"
                            checked={selectedAgentIds.includes(a.id)}
                            onChange={() => toggleAgent(a.id)}
                            aria-label={`Assigner ${a.email}`}
                          />
                        </td>
                        <td>{a.email}</td>
                        <td>{a.display_name || "—"}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
            <button
              type="submit"
              className={styles.submit}
              disabled={busy}
              style={{ marginTop: "1rem" }}
            >
              Enregistrer les agents
            </button>
            <p className={styles.muted} style={{ marginTop: "0.75rem" }}>
              Remplace toute l’affectation existante.
            </p>
          </form>
        ) : (
          <p className={styles.muted}>
            La liste des utilisateurs (sélection multi-agents) est réservée aux
            administrateurs. Les superviseurs peuvent changer le statut,
            importer des CSV et gérer les dispositions.
          </p>
        )}
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Import CSV</h2>
        <form className={styles.form} onSubmit={onImport}>
          <label>
            Nom de liste
            <input
              value={csvName}
              onChange={(e) => setCsvName(e.target.value)}
            />
          </label>
          <label>
            Fichier
            <input
              type="file"
              accept=".csv,text/csv"
              onChange={(e) => setCsvFile(e.target.files?.[0] ?? null)}
            />
          </label>
          <button
            type="submit"
            className={styles.submit}
            disabled={busy || !csvFile}
          >
            {busy ? "Import…" : "Importer"}
          </button>
        </form>
        {importSummary ? (
          <p className={styles.ok} style={{ marginTop: "1rem" }}>
            {importSummary}
          </p>
        ) : null}
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Dispositions</h2>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Code</th>
                <th>Libellé</th>
                <th>Contact</th>
                <th>Succès</th>
              </tr>
            </thead>
            <tbody>
              {dispositions.length === 0 ? (
                <tr>
                  <td colSpan={4} className={styles.muted}>
                    Aucune disposition.
                  </td>
                </tr>
              ) : (
                dispositions.map((d) => (
                  <tr key={d.id}>
                    <td>{d.code}</td>
                    <td>{d.label}</td>
                    <td>{d.is_contact ? "Oui" : "Non"}</td>
                    <td>{d.is_success ? "Oui" : "Non"}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        <h3 className={styles.sectionTitle} style={{ marginTop: "1.5rem" }}>
          Ajouter
        </h3>
        <form className={styles.form} onSubmit={onCreateDispo}>
          <label>
            Code
            <input
              required
              value={dispoForm.code}
              onChange={(e) =>
                setDispoForm({ ...dispoForm, code: e.target.value })
              }
            />
          </label>
          <label>
            Libellé
            <input
              required
              value={dispoForm.label}
              onChange={(e) =>
                setDispoForm({ ...dispoForm, label: e.target.value })
              }
            />
          </label>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={dispoForm.is_contact}
              onChange={(e) =>
                setDispoForm({ ...dispoForm, is_contact: e.target.checked })
              }
            />
            Contact
          </label>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={dispoForm.is_success}
              onChange={(e) =>
                setDispoForm({ ...dispoForm, is_success: e.target.checked })
              }
            />
            Succès
          </label>
          <button type="submit" className={styles.submit} disabled={busy}>
            Ajouter la disposition
          </button>
        </form>
      </section>
    </>
  );
}
