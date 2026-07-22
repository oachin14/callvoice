"use client";

import Link from "next/link";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  ApiError,
  Campaign,
  Carrier,
  createCampaign,
  listCampaigns,
  parseApiMessage,
  User,
} from "@/lib/api";
import styles from "../admin.module.css";

const STATUS_LABEL: Record<Campaign["status"], string> = {
  draft: "Brouillon",
  running: "En cours",
  paused: "En pause",
  stopped: "Arrêtée",
};

export default function CampaignsPage() {
  const router = useRouter();
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [carriers, setCarriers] = useState<Carrier[]>([]);
  const [name, setName] = useState("");
  const [carrierId, setCarrierId] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [isAdmin, setIsAdmin] = useState(false);

  const load = useCallback(async () => {
    setError("");
    try {
      const me = await api<User>("/auth/me");
      if (me.role !== "admin" && me.role !== "supervisor") {
        router.replace(me.role === "agent" ? "/agent" : "/login");
        return;
      }
      setIsAdmin(me.role === "admin");
      const list = await listCampaigns();
      setCampaigns(list);
      if (me.role === "admin") {
        try {
          const c = await api<Carrier[]>("/admin/carriers");
          setCarriers(c);
          if (!carrierId && c.length > 0) setCarrierId(c[0].id);
        } catch {
          /* carriers admin-only */
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
  }, [router, carrierId]);

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- initial load only
  }, []);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      const created = await createCampaign({
        name: name.trim(),
        carrier_id: carrierId,
      });
      setName("");
      router.push(`/campaigns/${created.id}`);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return <p className={styles.muted}>Chargement…</p>;
  }

  return (
    <>
      <header className={styles.pageHeader}>
        <h1 className={styles.title}>Campagnes</h1>
        <p className={styles.muted}>
          Création, statuts, agents et listes d’appels.
        </p>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Liste</h2>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Nom</th>
                <th>Statut</th>
                <th>Mode</th>
                <th>Créée</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {campaigns.length === 0 ? (
                <tr>
                  <td colSpan={5} className={styles.muted}>
                    Aucune campagne.
                  </td>
                </tr>
              ) : (
                campaigns.map((c) => (
                  <tr key={c.id}>
                    <td>{c.name}</td>
                    <td>{STATUS_LABEL[c.status] || c.status}</td>
                    <td>{c.dial_mode}</td>
                    <td>{new Date(c.created_at).toLocaleString("fr-FR")}</td>
                    <td>
                      <Link className={styles.ghost} href={`/campaigns/${c.id}`}>
                        Ouvrir
                      </Link>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      {isAdmin && carriers.length > 0 ? (
        <section className={styles.section}>
          <h2 className={styles.sectionTitle}>Nouvelle campagne</h2>
          <form className={styles.form} onSubmit={onCreate}>
            <label>
              Nom
              <input
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <label>
              Carrier
              <select
                required
                value={carrierId}
                onChange={(e) => setCarrierId(e.target.value)}
              >
                <option value="" disabled>
                  Sélectionner…
                </option>
                {carriers.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="submit"
              className={styles.submit}
              disabled={saving || !carrierId}
            >
              {saving ? "Création…" : "Créer la campagne"}
            </button>
          </form>
        </section>
      ) : !isAdmin ? (
        <section className={styles.section}>
          <h2 className={styles.sectionTitle}>Nouvelle campagne</h2>
          <form className={styles.form} onSubmit={onCreate}>
            <label>
              Nom
              <input
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <label>
              Carrier ID
              <input
                required
                value={carrierId}
                onChange={(e) => setCarrierId(e.target.value)}
                placeholder="UUID du carrier"
              />
            </label>
            <button
              type="submit"
              className={styles.submit}
              disabled={saving || !carrierId.trim()}
            >
              {saving ? "Création…" : "Créer la campagne"}
            </button>
          </form>
        </section>
      ) : (
        <section className={styles.section}>
          <p className={styles.muted}>
            Aucun carrier configuré — créez-en un avant d’ajouter une campagne.
          </p>
        </section>
      )}
    </>
  );
}
