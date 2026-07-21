import {
  Invitation,
  Registerer,
  RegistererState,
  Session,
  SessionState,
  UserAgent,
  type SessionInviteOptions,
} from "sip.js";

export type SoftphoneConfig = {
  wssUrl: string;
  sipUri: string;
  password: string;
  iceServers?: { urls: string[] }[];
};

export type SoftphoneCallbacks = {
  onInvite?: (invitation: Invitation) => void;
  onBye?: () => void;
  onRegistered?: () => void;
  onUnregistered?: () => void;
  onError?: (error: Error) => void;
};

type AudioSdh = {
  remoteMediaStream: MediaStream;
  enableSenderTracks(enable: boolean): void;
  enableReceiverTracks(enable: boolean): void;
};

/**
 * Browser softphone: SIP.js UserAgent over FreeSWITCH WSS.
 * Outbound dialing is server-originate (edge CPS); this endpoint answers INVITEs + media controls.
 */
export class Softphone {
  private ua: UserAgent | null = null;
  private registerer: Registerer | null = null;
  private session: Session | null = null;
  private held = false;
  private muted = false;
  private remoteAudio: HTMLAudioElement | null = null;
  private callbacks: SoftphoneCallbacks = {};

  setRemoteAudio(el: HTMLAudioElement | null) {
    this.remoteAudio = el;
  }

  setCallbacks(cb: SoftphoneCallbacks) {
    this.callbacks = cb;
  }

  get currentSession(): Session | null {
    return this.session;
  }

  get isHeld(): boolean {
    return this.held;
  }

  get isMuted(): boolean {
    return this.muted;
  }

  async connect(config: SoftphoneConfig): Promise<void> {
    await this.disconnect();

    const uri = UserAgent.makeURI(config.sipUri);
    if (!uri) {
      throw new Error(`Invalid SIP URI: ${config.sipUri}`);
    }

    const iceServers = (config.iceServers ?? []).map((s) => ({
      urls: s.urls,
    }));

    const ua = new UserAgent({
      uri,
      transportOptions: {
        server: config.wssUrl,
      },
      authorizationUsername: uri.user ?? undefined,
      authorizationPassword: config.password,
      sessionDescriptionHandlerFactoryOptions: {
        iceGatheringTimeout: 5000,
        peerConnectionConfiguration: {
          iceServers,
        },
      },
      delegate: {
        onInvite: (invitation: Invitation) => {
          this.bindSession(invitation);
          this.callbacks.onInvite?.(invitation);
        },
      },
    });

    this.ua = ua;
    await ua.start();

    const registerer = new Registerer(ua);
    this.registerer = registerer;

    registerer.stateChange.addListener((state) => {
      if (state === RegistererState.Registered) {
        this.callbacks.onRegistered?.();
      }
      if (state === RegistererState.Unregistered) {
        this.callbacks.onUnregistered?.();
      }
    });

    await registerer.register();
  }

  async disconnect(): Promise<void> {
    try {
      if (this.session) {
        await this.hangup();
      }
    } catch {
      /* best-effort */
    }

    try {
      if (this.registerer) {
        await this.registerer.unregister();
      }
    } catch {
      /* best-effort */
    }

    try {
      if (this.ua) {
        await this.ua.stop();
      }
    } catch {
      /* best-effort */
    }

    this.registerer = null;
    this.ua = null;
    this.session = null;
    this.held = false;
    this.muted = false;
  }

  async answer(invitation?: Invitation): Promise<void> {
    const inv = invitation ?? (this.session instanceof Invitation ? this.session : null);
    if (!inv || !(inv instanceof Invitation)) {
      throw new Error("No invitation to answer");
    }
    this.bindSession(inv);
    await inv.accept({
      sessionDescriptionHandlerOptions: {
        constraints: { audio: true, video: false },
      },
    });
    this.attachRemoteAudio(inv);
  }

  async hangup(): Promise<void> {
    const session = this.session;
    if (!session) return;

    switch (session.state) {
      case SessionState.Initial:
      case SessionState.Establishing:
        if (session instanceof Invitation) {
          await session.reject();
        } else {
          await session.bye();
        }
        break;
      case SessionState.Established:
        await session.bye();
        break;
      default:
        break;
    }
    this.clearSession();
  }

  mute(muted = true): void {
    const session = this.requireEstablished();
    const sdh = this.sdh(session);
    this.muted = muted;
    sdh.enableSenderTracks(!this.held && !this.muted);
  }

  unmute(): void {
    this.mute(false);
  }

  async hold(held = true): Promise<void> {
    const session = this.requireEstablished();
    if (this.held === held) return;

    const options: SessionInviteOptions = {
      requestDelegate: {
        onAccept: () => {
          this.held = held;
          const sdh = this.sdh(session);
          sdh.enableReceiverTracks(!this.held);
          sdh.enableSenderTracks(!this.held && !this.muted);
        },
        onReject: () => {
          this.held = !held;
          const sdh = this.sdh(session);
          sdh.enableReceiverTracks(!this.held);
          sdh.enableSenderTracks(!this.held && !this.muted);
        },
      },
    };

    const reInviteOpts = {
      ...(session.sessionDescriptionHandlerOptionsReInvite ?? {}),
      hold: held,
    };
    session.sessionDescriptionHandlerOptionsReInvite = reInviteOpts;

    this.held = held;
    try {
      await session.invite(options);
      const sdh = this.sdh(session);
      sdh.enableReceiverTracks(!this.held);
      sdh.enableSenderTracks(!this.held && !this.muted);
    } catch (err) {
      this.held = !held;
      throw err;
    }
  }

  async unhold(): Promise<void> {
    await this.hold(false);
  }

  async sendDTMF(digit: string): Promise<void> {
    const session = this.requireEstablished();
    if (!/^[0-9A-D#*,]$/.test(digit)) {
      throw new Error(`Invalid DTMF tone: ${digit}`);
    }
    await session.info({
      requestOptions: {
        body: {
          contentDisposition: "render",
          contentType: "application/dtmf-relay",
          content: `Signal=${digit}\r\nDuration=2000`,
        },
      },
    });
  }

  private bindSession(session: Session) {
    if (this.session && this.session !== session) {
      // Replace ringing/prior session.
      this.clearSession(false);
    }
    this.session = session;
    this.held = false;
    this.muted = false;

    session.stateChange.addListener((state) => {
      if (state === SessionState.Established) {
        this.attachRemoteAudio(session);
      }
      if (state === SessionState.Terminated) {
        this.clearSession();
        this.callbacks.onBye?.();
      }
    });
  }

  private clearSession(emitBye = false) {
    this.session = null;
    this.held = false;
    this.muted = false;
    if (this.remoteAudio) {
      this.remoteAudio.srcObject = null;
    }
    if (emitBye) {
      this.callbacks.onBye?.();
    }
  }

  private requireEstablished(): Session {
    const session = this.session;
    if (!session || session.state !== SessionState.Established) {
      throw new Error("No established session");
    }
    return session;
  }

  private sdh(session: Session): AudioSdh {
    const handler = session.sessionDescriptionHandler as Partial<AudioSdh> | undefined;
    if (
      !handler ||
      typeof handler.enableSenderTracks !== "function" ||
      typeof handler.enableReceiverTracks !== "function" ||
      !handler.remoteMediaStream
    ) {
      throw new Error("Session description handler unavailable");
    }
    return handler as AudioSdh;
  }

  private attachRemoteAudio(session: Session) {
    if (!this.remoteAudio) return;
    try {
      const sdh = this.sdh(session);
      this.remoteAudio.srcObject = sdh.remoteMediaStream;
      void this.remoteAudio.play().catch(() => {
        /* autoplay may require a user gesture */
      });
    } catch {
      /* media not ready yet */
    }
  }
}
