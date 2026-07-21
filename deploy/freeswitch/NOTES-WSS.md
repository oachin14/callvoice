# FreeSWITCH WSS (WebRTC) lab notes

Agent softphone (SIP.js) registers to FreeSWITCH over **WSS**, not to `callvoice-edge`.

| Setting | Lab default |
|---------|-------------|
| Edge env | `FREESWITCH_WSS_URL=wss://localhost:7443` |
| Profile stub | `conf/sip_profiles/internal.xml` (`wss-binding` `:7443`) |
| Auth | Directory user `agent-{uuid}` provisioned by edge (`webrtccred`) |

## Checklist

1. Copy/merge `internal.xml` into the running FS `sip_profiles/` (Dockerfile does this).
2. Ensure TLS material exists under `/etc/freeswitch/tls` (self-signed OK for lab).
3. Confirm: `sofia status profile internal` shows WSS listening on 7443.
4. Browser must trust (or ignore) the cert for `wss://localhost:7443`.

Without a live WSS listener, Connect reaches “connecting” then fails SIP register — UI still builds; lab headset E2E is blocked until FS WSS is up.
