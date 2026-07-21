"use client";

import { Invitation } from "sip.js";
import styles from "./agent.module.css";

const DTMF_KEYS = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"] as const;

type SoftphonePanelProps = {
  ringing: Invitation | null;
  inCall: boolean;
  muted: boolean;
  held: boolean;
  busy: boolean;
  onAnswer: () => void;
  onReject: () => void;
  onHangup: () => void;
  onMuteToggle: () => void;
  onHoldToggle: () => void;
  onDtmf: (digit: string) => void;
};

export function SoftphonePanel({
  ringing,
  inCall,
  muted,
  held,
  busy,
  onAnswer,
  onReject,
  onHangup,
  onMuteToggle,
  onHoldToggle,
  onDtmf,
}: SoftphonePanelProps) {
  if (ringing) {
    return (
      <div className={styles.softphone} aria-live="polite">
        <p className={styles.softphoneTitle}>Appel entrant</p>
        <p className={styles.softphoneSub}>
          {ringing.remoteIdentity.uri.toString()}
        </p>
        <div className={styles.actions}>
          <button type="button" disabled={busy} onClick={onAnswer}>
            Décrocher
          </button>
          <button type="button" disabled={busy} onClick={onReject}>
            Refuser
          </button>
        </div>
      </div>
    );
  }

  if (!inCall) {
    return null;
  }

  return (
    <div className={styles.softphone} aria-live="polite">
      <p className={styles.softphoneTitle}>En communication</p>
      <div className={styles.actions}>
        <button type="button" disabled={busy} onClick={onMuteToggle}>
          {muted ? "Activer micro" : "Couper micro"}
        </button>
        <button type="button" disabled={busy} onClick={onHoldToggle}>
          {held ? "Reprendre" : "Mise en attente"}
        </button>
        <button type="button" disabled={busy} onClick={onHangup}>
          Raccrocher
        </button>
      </div>
      <div className={styles.dtmf} role="group" aria-label="Clavier DTMF">
        {DTMF_KEYS.map((digit) => (
          <button
            key={digit}
            type="button"
            className={styles.dtmfKey}
            disabled={busy || held}
            onClick={() => onDtmf(digit)}
          >
            {digit}
          </button>
        ))}
      </div>
    </div>
  );
}
