"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  ApiError,
  CreateUserInput,
  parseApiMessage,
  User,
} from "@/lib/api";
import styles from "../admin.module.css";

const emptyForm = {
  email: "",
  password: "",
  role: "agent" as CreateUserInput["role"],
  display_name: "",
};

export default function UsersPage() {
  const router = useRouter();
  const [me, setMe] = useState<User | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [form, setForm] = useState(emptyForm);
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [accessDenied, setAccessDenied] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setError("");
    setAccessDenied(false);
    try {
      const current = await api<User>("/auth/me");
      if (current.role !== "admin") {
        setAccessDenied(true);
        return;
      }
      setMe(current);
      const list = await api<User[]>("/admin/users");
      setUsers(list);
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
    setOk("");
    try {
      const payload: CreateUserInput = {
        email: form.email.trim(),
        password: form.password,
        role: form.role,
      };
      if (form.display_name.trim()) {
        payload.display_name = form.display_name.trim();
      }
      await api<User>("/admin/users", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      setForm(emptyForm);
      setOk("Utilisateur créé.");
      await load();
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function onToggleDisabled(u: User) {
    setError("");
    setOk("");
    try {
      await api<User>(`/admin/users/${u.id}`, {
        method: "PATCH",
        body: JSON.stringify({ disabled: !u.disabled }),
      });
      setOk(u.disabled ? "Compte réactivé." : "Compte désactivé.");
      await load();
    } catch (err) {
      setError(parseApiMessage(err));
    }
  }

  async function onChangeRole(u: User, role: User["role"]) {
    if (role === u.role) return;
    setError("");
    setOk("");
    try {
      await api<User>(`/admin/users/${u.id}`, {
        method: "PATCH",
        body: JSON.stringify({ role }),
      });
      setOk("Rôle mis à jour.");
      await load();
    } catch (err) {
      setError(parseApiMessage(err));
    }
  }

  async function onResetPassword(u: User) {
    const password = window.prompt(`Nouveau mot de passe pour ${u.email}`);
    if (!password) return;
    setError("");
    setOk("");
    try {
      await api<{ status: string }>(`/admin/users/${u.id}/reset-password`, {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      setOk("Mot de passe réinitialisé.");
    } catch (err) {
      setError(parseApiMessage(err));
    }
  }

  if (loading) {
    return <p className={styles.muted}>Chargement…</p>;
  }

  if (accessDenied) {
    return (
      <p className={styles.error}>Accès réservé aux administrateurs.</p>
    );
  }

  return (
    <>
      <header className={styles.pageHeader}>
        <h1 className={styles.title}>Utilisateurs</h1>
        <p className={styles.muted}>
          {me
            ? `Gestion des comptes — ${me.email}`
            : "Création, rôles et désactivation"}
        </p>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}
      {ok ? <p className={styles.ok}>{ok}</p> : null}

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Comptes</h2>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>E-mail</th>
                <th>Nom</th>
                <th>Rôle</th>
                <th>2FA</th>
                <th>Actif</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {users.length === 0 ? (
                <tr>
                  <td colSpan={6} className={styles.muted}>
                    Aucun utilisateur.
                  </td>
                </tr>
              ) : (
                users.map((u) => (
                  <tr key={u.id}>
                    <td>{u.email}</td>
                    <td>{u.display_name || "—"}</td>
                    <td>
                      <select
                        value={u.role}
                        onChange={(e) =>
                          void onChangeRole(u, e.target.value as User["role"])
                        }
                        aria-label={`Rôle de ${u.email}`}
                      >
                        <option value="admin">admin</option>
                        <option value="supervisor">supervisor</option>
                        <option value="agent">agent</option>
                      </select>
                    </td>
                    <td>{u.totp_enabled ? "Oui" : "Non"}</td>
                    <td>
                      <button
                        type="button"
                        className={styles.chip}
                        onClick={() => void onToggleDisabled(u)}
                      >
                        {u.disabled ? "Non" : "Oui"}
                      </button>
                    </td>
                    <td>
                      <div className={styles.rowActions}>
                        <button
                          type="button"
                          className={styles.ghost}
                          onClick={() => void onResetPassword(u)}
                        >
                          Mot de passe
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>Créer un utilisateur</h2>
        <form className={styles.form} onSubmit={onCreate}>
          <label>
            E-mail
            <input
              type="email"
              required
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
            />
          </label>
          <label>
            Mot de passe
            <input
              type="password"
              required
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
            />
          </label>
          <label>
            Rôle
            <select
              value={form.role}
              onChange={(e) =>
                setForm({
                  ...form,
                  role: e.target.value as CreateUserInput["role"],
                })
              }
            >
              <option value="agent">agent</option>
              <option value="supervisor">supervisor</option>
              <option value="admin">admin</option>
            </select>
          </label>
          <label>
            Nom affiché
            <input
              value={form.display_name}
              onChange={(e) =>
                setForm({ ...form, display_name: e.target.value })
              }
              placeholder="optionnel"
            />
          </label>
          <button type="submit" className={styles.submit} disabled={saving}>
            {saving ? "Création…" : "Créer l’utilisateur"}
          </button>
        </form>
      </section>
    </>
  );
}
