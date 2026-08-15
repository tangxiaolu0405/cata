/** Wails runtime + Go bindings (injected at runtime). */
export type Mood =
  | "idle"
  | "thinking"
  | "tool"
  | "error"
  | "confirm"
  | "listening";

export interface PetStatus {
  cwd: string;
  always_on_top: boolean;
  busy: boolean;
  socket: string;
}

export interface ConfirmPayload {
  confirm_id: string;
  command_line: string;
  cwd: string;
}

export interface HitRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

declare global {
  interface Window {
    runtime?: {
      EventsOn(event: string, callback: (...data: any[]) => void): () => void;
      EventsEmit(event: string, ...data: any[]): void;
    };
    go?: {
      pet?: {
        App?: {
          Send(text: string): Promise<void>;
          RespondExec(confirmID: string, approved: boolean): Promise<void>;
          RespondChoice(choiceID: string, selected: string[]): Promise<void>;
          Cancel(): Promise<void>;
          SetCwd(cwd: string): Promise<void>;
          GetCwd(): Promise<string>;
          SetAlwaysOnTop(on: boolean): Promise<void>;
          GetAlwaysOnTop(): Promise<boolean>;
          SetClickThrough(ignore: boolean): Promise<void>;
          SetForceSolid(on: boolean): Promise<void>;
          SetHitRegions(rects: HitRect[]): Promise<void>;
          StartDrag(): Promise<void>;
          MoveWindowBy(dx: number, dy: number): Promise<void>;
          ResizeWindow(width: number, height: number): Promise<void>;
          Status(): Promise<PetStatus>;
        };
      };
    };
  }
}

export function eventsOn(event: string, cb: (...data: any[]) => void): () => void {
  if (window.runtime?.EventsOn) {
    return window.runtime.EventsOn(event, cb);
  }
  return () => {};
}

export function app() {
  const a = window.go?.pet?.App;
  if (!a) {
    throw new Error("Wails App bindings unavailable (run inside cata-pet)");
  }
  return a;
}

export async function safeApp() {
  return window.go?.pet?.App ?? null;
}
