"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { ReactNode, useCallback, useEffect, useState } from "react";
import { api, ApiError, parseApiMessage, User } from "@/lib/api";
import styles from "./admin.module.css";

type NavItem = {
  href: string;
  label: string;
  roles: Array<User["role"]>;
};

const NAV: NavItem[] = [
  { href: "/carriers", label: "Carriers", roles: ["admin"] },
  { href: "/users", label: "Utilisateurs", roles: ["admin"] },
  { href: "/campaigns", label: "Campagnes", roles: ["admin", "supervisor"] },
  { href: "/live", label: "Live", roles: ["admin", "supervisor"] },
  { href: "/reports", label: "Rapports", roles: ["admin", "supervisor"] },
  { href: "/agent", label: "Console agent", roles: ["admin", "supervisor", "agent"] },
];

export default function AdminLayout({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [user, setUser] = useState<User | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setError("");
    try {
      const me = await api<User>("/auth/me");
      if (me.role === "agent") {
        router.replace("/agent");
        return;
      }
      setUser(me);
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

  async function onLogout() {
    try {
      await api("/auth/logout", { method: "POST", body: "{}" });
    } finally {
      router.push("/login");
    }
  }

  if (loading) {
    return (
      <div className={styles.shell}>
        <aside className={styles.sidebar}>
          <p className={styles.brand}>CallVoice</p>
        </aside>
        <div className={`${styles.main} ${styles.centerMsg}`}>
          <p className={styles.muted}>Chargement…</p>
        </div>
      </div>
    );
  }

  if (!user) {
    return (
      <div className={styles.shell}>
        <aside className={styles.sidebar}>
          <p className={styles.brand}>CallVoice</p>
        </aside>
        <div className={`${styles.main} ${styles.centerMsg}`}>
          {error ? <p className={styles.error}>{error}</p> : null}
          <p className={styles.muted}>Session requise.</p>
        </div>
      </div>
    );
  }

  const links = NAV.filter((item) => item.roles.includes(user.role));

  return (
    <div className={styles.shell}>
      <aside className={styles.sidebar}>
        <p className={styles.brand}>CallVoice</p>
        <p className={styles.userMeta}>
          {user.display_name || user.email}
          <br />
          {user.role}
        </p>
        <nav className={styles.nav} aria-label="Navigation admin">
          {links.map((item) => {
            const active =
              item.href === "/agent"
                ? false
                : pathname === item.href || pathname.startsWith(`${item.href}/`);
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`${styles.navLink} ${active ? styles.navLinkActive : ""}`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
        <button type="button" className={styles.logout} onClick={onLogout}>
          Déconnexion
        </button>
      </aside>
      <div className={styles.main}>{children}</div>
    </div>
  );
}
