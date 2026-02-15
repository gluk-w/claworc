declare module "@novnc/novnc" {
  interface RFBCredentials {
    username?: string;
    password?: string;
    target?: string;
  }

  interface RFBCapabilities {
    power: boolean;
  }

  type RFBEventMap = {
    connect: CustomEvent;
    disconnect: CustomEvent<{ clean: boolean }>;
    credentialsrequired: CustomEvent;
    securityfailure: CustomEvent<{ status: number; reason: string }>;
    clipboard: CustomEvent<{ text: string }>;
    bell: CustomEvent;
    desktopname: CustomEvent<{ name: string }>;
    capabilities: CustomEvent<{ capabilities: RFBCapabilities }>;
  };

  export default class RFB extends EventTarget {
    constructor(
      target: HTMLElement,
      urlOrChannel: string | WebSocket,
      options?: {
        shared?: boolean;
        credentials?: RFBCredentials;
        repeaterID?: string;
        wsProtocols?: string[];
      },
    );

    // Properties
    viewOnly: boolean;
    focusOnClick: boolean;
    clipViewport: boolean;
    dragViewport: boolean;
    scaleViewport: boolean;
    resizeSession: boolean;
    showDotCursor: boolean;
    background: string;
    qualityLevel: number;
    compressionLevel: number;
    readonly capabilities: RFBCapabilities;

    // Methods
    disconnect(): void;
    sendCredentials(credentials: RFBCredentials): void;
    sendKey(keysym: number, code: string | null, down?: boolean): void;
    sendCtrlAltDel(): void;
    focus(options?: FocusOptions): void;
    blur(): void;
    machineShutdown(): void;
    machineReboot(): void;
    machineReset(): void;
    clipboardPasteFrom(text: string): void;

    // Typed addEventListener
    addEventListener<K extends keyof RFBEventMap>(
      type: K,
      listener: (ev: RFBEventMap[K]) => void,
      options?: boolean | AddEventListenerOptions,
    ): void;
  }
}
