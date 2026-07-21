"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, LoginResponse, User } from "@/lib/api";
import styles from "./login.module.css";

type Step = "credentials" | "totp" | "setup";

function parseApiMessage(err: unknown): string {
  if (err instanceof ApiError) {
    try {
      const parsed = JSON.parse(err.body) as { error?: string };
      switch (parsed.error) {
        case "invalid_credentials":
          return "Identifiants incorrects.";
        case "account_locked":
          return "Compte temporairement verrouillé. Réessayez plus tard.";
        case "invalid_totp":
          return "Code d’authentification invalide.";
        case "pending_required":
        case "pending_invalid":
          return "Session 2FA expirée. Reconnectez-vous.";
        default:
          return parsed.error || "Une erreur est survenue.";
      }
    } catch {
      return err.message || "Une erreur est survenue.";
    }
  }
  if (err instanceof Error) return err.message;
  return "Une erreur est survenue.";
}

export default function LoginPage() {
  const router = useRouter();
  const [step, setStep] = useState<Step>("credentials");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [setupSecret, setSetupSecret] = useState("");
  const [otpauthUrl, setOtpauthUrl] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function redirectAfterAuth(user?: User) {
    if (user?.role === "admin") {
      router.push("/carriers");
      return;
    }
    router.push("/");
  }

  async function onCredentials(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api<LoginResponse>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
      });

      if (res.status === "totp_required") {
        setStep("totp");
        setTotpCode("");
        return;
      }

      if (res.status === "totp_setup_required") {
        setStep("setup");
        await startSetup();
        return;
      }

      redirectAfterAuth(res.user);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function onVerifyTotp(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api<{ status: string; user: User }>("/auth/2fa/verify", {
        method: "POST",
        body: JSON.stringify({ code: totpCode.trim() }),
      });
      redirectAfterAuth(res.user);
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function startSetup() {
    const setup = await api<{ secret: string; otpauth_url: string }>(
      "/auth/2fa/setup",
      { method: "POST", body: "{}" },
    );
    setSetupSecret(setup.secret);
    setOtpauthUrl(setup.otpauth_url);
  }

  async function onEnableTotp(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (!setupSecret) {
        await startSetup();
      }
      await api("/auth/2fa/enable", {
        method: "POST",
        body: JSON.stringify({ code: totpCode.trim() }),
      });
      setStep("totp");
      setTotpCode("");
      setError("");
      // After enable, session is cleared — password step already done; need re-login then totp.
      const login = await api<LoginResponse>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
      });
      if (login.status === "totp_required") {
        setStep("totp");
        return;
      }
      if (login.status === "ok") {
        redirectAfterAuth(login.user);
      }
    } catch (err) {
      setError(parseApiMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className={styles.shell}>
      <div className={styles.atmosphere} aria-hidden />
      <div className={styles.grid} aria-hidden />

      <section className={styles.panel}>
        <p className={styles.brand}>CallVoice</p>
        <h1 className={styles.headline}>
          {step === "credentials" && "Connexion"}
          {step === "totp" && "Vérification 2FA"}
          {step === "setup" && "Activer la 2FA"}
        </h1>
        <p className={styles.sub}>
          {step === "credentials" &&
            "Accédez à votre environnement téléphonique dédié."}
          {step === "totp" &&
            "Saisissez le code à 6 chiffres de votre application d’authentification."}
          {step === "setup" &&
            "La double authentification est obligatoire pour les administrateurs."}
        </p>

        {error ? <p className={styles.error} role="alert">{error}</p> : null}

        {step === "credentials" ? (
          <form className={styles.form} onSubmit={onCredentials}>
            <label className={styles.label}>
              E-mail
              <input
                className={styles.input}
                type="email"
                autoComplete="username"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </label>
            <label className={styles.label}>
              Mot de passe
              <input
                className={styles.input}
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            <button className={styles.submit} type="submit" disabled={busy}>
              {busy ? "Connexion…" : "Se connecter"}
            </button>
          </form>
        ) : null}

        {step === "totp" ? (
          <form className={styles.form} onSubmit={onVerifyTotp}>
            <label className={styles.label}>
              Code d’authentification
              <input
                className={`${styles.input} ${styles.otp}`}
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                autoComplete="one-time-code"
                required
                value={totpCode}
                onChange={(e) =>
                  setTotpCode(e.target.value.replace(/\D/g, "").slice(0, 6))
                }
              />
            </label>
            <button className={styles.submit} type="submit" disabled={busy}>
              {busy ? "Vérification…" : "Vérifier"}
            </button>
            <button
              type="button"
              className={styles.linkish}
              onClick={() => {
                setStep("credentials");
                setTotpCode("");
                setError("");
              }}
            >
              Retour
            </button>
          </form>
        ) : null}

        {step === "setup" ? (
          <form className={styles.form} onSubmit={onEnableTotp}>
            <div className={styles.setupBox}>
              <p>
                Scannez ce secret dans votre application TOTP, ou saisissez-le
                manuellement :
              </p>
              <code className={styles.secret}>{setupSecret || "…"}</code>
              {otpauthUrl ? (
                <a className={styles.otpauth} href={otpauthUrl}>
                  Ouvrir le lien otpauth
                </a>
              ) : null}
            </div>
            <label className={styles.label}>
              Code de confirmation
              <input
                className={`${styles.input} ${styles.otp}`}
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                value={totpCode}
                onChange={(e) =>
                  setTotpCode(e.target.value.replace(/\D/g, "").slice(0, 6))
                }
              />
            </label>
            <button className={styles.submit} type="submit" disabled={busy}>
              {busy ? "Activation…" : "Activer et continuer"}
            </button>
          </form>
        ) : null}
      </section>
    </main>
  );
}
