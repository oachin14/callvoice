"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  ApiError,
  Carrier,
  CreateCarrierInput,
  parseApiMessage,
  User,
} from "@/lib/api";
import styles from "./carriers.module.css";

const emptyForm = {
  name: "",
  host: "",
  port: "5060",
  transport: "udp",
  username: "",
  password: "",
  realm: "",
  codecs: "PCMU,PCMA",
  caller_ids: "",
  max_cps: "30",
  max_channels: "100",
  priority: "100",
  enabled: true,
};

function splitCSV(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export default function CarriersPage() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [carriers, setCarriers] = useState<Carrier[]>([]);
  const [form, setForm] = useState(emptyForm);
  const [error, setError] = useState("");
  const [accessDenied, setAccessDenied] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setError("");
    setAccessDenied(false);
    try {
      const me = await api<User>("/auth/me");
      if (me.role !== "admin") {
        setAccessDenied(true);
        return;
      }
      setUser(me);
      const list = await api<Carrier[]>("/admin/carriers");
      setCarriers(list);
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
    void load();
  }, [load]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      const payload: CreateCarrierInput = {
        name: form.name.trim(),
        host: form.host.trim(),
        port: Number(form.port) || 5060,
        transport: form.transport,
        max_cps: Number(form.max_cps) || 30,
        max_channels: Number(form.max_channels) || 100,
        priority: Number(form.priority) || 100,
        enabled: form.enabled,
        codecs: splitCSV(form.codecs),
        caller_ids: splitCSV(form.caller_ids),
      };
      if (form.username.trim()) payload.username = form.username.trim();
      if (form.password) payload.password = form.password;
      if (form.realm.trim()) payload.realm = form.realm.trim();

      await api<Carrier>("/admin/carriers", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      setForm(emptyForm);
      await load();
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function onToggle(c: Carrier) {
    setError("");
    try {
      await api<Carrier>(`/admin/carriers/${c.id}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: !c.enabled }),
      });
      await load();
    } catch (err) {
      setError(parseApiMessage(err));
    }
  }

  async function onDelete(id: string) {
    if (!window.confirm("Supprimer ce carrier ?")) return;
    setError("");
    try {
      await api<void>(`/admin/carriers/${id}`, { method: "DELETE" });
      await load();
    } catch (err) {
      setError(parseApiMessage(err));
    }
  }

  async function onLogout() {
    try {
      await api("/auth/logout", { method: "POST", body: "{}" });
    } finally {
      router.push("/login");
    }
  }

  if (loading) {
    return (
      <main className={styles.shell}>
        <p className={styles.muted}>Chargement…</p>
      </main>
    );
  }

  if (accessDenied) {
    return (
      <main className={styles.shell}>
        <p className={styles.error}>Accès réservé aux administrateurs.</p>
      </main>
    );
  }

  return (
    <main className={styles.shell}>
      <header className={styles.header}>
        <div>
          <p className={styles.brand}>CallVoice</p>
          <h1 className={styles.title}>Carriers BYOC</h1>
          <p className={styles.muted}>
            {user
              ? `Connecté en tant que ${user.email}`
              : "Administration des trunks SIP"}
          </p>
        </div>
        <div className={styles.actions}>
          <a className={styles.ghost} href="/login">
            Connexion
          </a>
          <button type="button" className={styles.ghost} onClick={onLogout}>
            Déconnexion
          </button>
        </div>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Trunks configurés</h2>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Nom</th>
                <th>Host</th>
                <th>Port</th>
                <th>Transport</th>
                <th>Auth</th>
                <th>Max CPS</th>
                <th>Canaux</th>
                <th>Priorité</th>
                <th>Actif</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {carriers.length === 0 ? (
                <tr>
                  <td colSpan={10} className={styles.muted}>
                    Aucun carrier pour le moment.
                  </td>
                </tr>
              ) : (
                carriers.map((c) => (
                  <tr key={c.id}>
                    <td>{c.name}</td>
                    <td>{c.host}</td>
                    <td>{c.port}</td>
                    <td>{c.transport.toUpperCase()}</td>
                    <td>
                      {c.username
                        ? `${c.username}${c.password_set ? " · ••••" : ""}`
                        : "IP / ACL"}
                    </td>
                    <td>{c.max_cps}</td>
                    <td>{c.max_channels}</td>
                    <td>{c.priority}</td>
                    <td>
                      <button
                        type="button"
                        className={styles.chip}
                        onClick={() => void onToggle(c)}
                      >
                        {c.enabled ? "Oui" : "Non"}
                      </button>
                    </td>
                    <td>
                      <button
                        type="button"
                        className={styles.danger}
                        onClick={() => void onDelete(c.id)}
                      >
                        Supprimer
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Ajouter un carrier</h2>
        <form className={styles.form} onSubmit={onCreate}>
          <label>
            Nom
            <input
              required
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </label>
          <label>
            Host
            <input
              required
              value={form.host}
              onChange={(e) => setForm({ ...form, host: e.target.value })}
            />
          </label>
          <label>
            Port
            <input
              type="number"
              min={1}
              max={65535}
              value={form.port}
              onChange={(e) => setForm({ ...form, port: e.target.value })}
            />
          </label>
          <label>
            Transport
            <select
              value={form.transport}
              onChange={(e) => setForm({ ...form, transport: e.target.value })}
            >
              <option value="udp">UDP</option>
              <option value="tcp">TCP</option>
              <option value="tls">TLS</option>
            </select>
          </label>
          <label>
            Username
            <input
              value={form.username}
              onChange={(e) => setForm({ ...form, username: e.target.value })}
              placeholder="optionnel"
            />
          </label>
          <label>
            Password
            <input
              type="password"
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
              placeholder="optionnel"
            />
          </label>
          <label>
            Realm
            <input
              value={form.realm}
              onChange={(e) => setForm({ ...form, realm: e.target.value })}
              placeholder="optionnel"
            />
          </label>
          <label>
            Codecs
            <input
              value={form.codecs}
              onChange={(e) => setForm({ ...form, codecs: e.target.value })}
              placeholder="PCMU,PCMA"
            />
          </label>
          <label>
            Caller IDs
            <input
              value={form.caller_ids}
              onChange={(e) => setForm({ ...form, caller_ids: e.target.value })}
              placeholder="+33123456789"
            />
          </label>
          <label>
            Max CPS
            <input
              type="number"
              min={1}
              value={form.max_cps}
              onChange={(e) => setForm({ ...form, max_cps: e.target.value })}
            />
          </label>
          <label>
            Max canaux
            <input
              type="number"
              min={1}
              value={form.max_channels}
              onChange={(e) =>
                setForm({ ...form, max_channels: e.target.value })
              }
            />
          </label>
          <label>
            Priorité
            <input
              type="number"
              value={form.priority}
              onChange={(e) => setForm({ ...form, priority: e.target.value })}
            />
          </label>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            Activé
          </label>
          <button type="submit" className={styles.submit} disabled={saving}>
            {saving ? "Enregistrement…" : "Créer le carrier"}
          </button>
        </form>
      </section>
    </main>
  );
}
