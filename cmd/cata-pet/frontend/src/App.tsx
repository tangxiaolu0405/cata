import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Cat } from "./Cat";
import {
  eventsOn,
  safeApp,
  type ConfirmPayload,
  type HitRect,
  type Mood,
} from "./wails";
import { getSpeechRecognitionCtor, startVoiceSession } from "./voice";
import "./app.css";

type Msg = { role: "user" | "assistant" | "system"; text: string };

function collectHitRects(root: HTMLElement): HitRect[] {
  const nodes = root.querySelectorAll<HTMLElement>("[data-hit]");
  const out: HitRect[] = [];
  nodes.forEach((el) => {
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) return;
    // Pad slightly so the cursor near the outline still engages.
    const pad = 6;
    out.push({
      x: r.left - pad,
      y: r.top - pad,
      w: r.width + pad * 2,
      h: r.height + pad * 2,
    });
  });
  return out;
}

export default function App() {
  const [expanded, setExpanded] = useState(false);
  const [mood, setMood] = useState<Mood>("idle");
  const [messages, setMessages] = useState<Msg[]>([]);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [alwaysOnTop, setAlwaysOnTop] = useState(true);
  const [confirm, setConfirm] = useState<ConfirmPayload | null>(null);
  const [choice, setChoice] = useState<any | null>(null);
  const [voiceHint, setVoiceHint] = useState("");
  const [listening, setListening] = useState(false);
  const assistantBuf = useRef("");
  const shellRef = useRef<HTMLDivElement>(null);
  const stopVoiceRef = useRef<(() => void) | null>(null);
  const voiceSupported = !!getSpeechRecognitionCtor();

  const syncHits = useCallback(async () => {
    const root = shellRef.current;
    if (!root) return;
    const a = await safeApp();
    await a?.SetHitRegions(collectHitRects(root));
  }, []);

  useLayoutEffect(() => {
    void syncHits();
  }, [expanded, mood, messages.length, confirm, choice, syncHits]);

  useEffect(() => {
    const id = window.setInterval(() => void syncHits(), 400);
    const onResize = () => void syncHits();
    window.addEventListener("resize", onResize);
    return () => {
      window.clearInterval(id);
      window.removeEventListener("resize", onResize);
    };
  }, [syncHits]);

  useEffect(() => {
    const offs = [
      eventsOn("pet:mood", (m: Mood) => setMood(m)),
      eventsOn("pet:token", (t: string) => {
        assistantBuf.current += t;
        const snap = assistantBuf.current;
        setMessages((prev) => {
          const next = [...prev];
          const last = next[next.length - 1];
          if (last?.role === "assistant") {
            next[next.length - 1] = { role: "assistant", text: snap };
          } else {
            next.push({ role: "assistant", text: snap });
          }
          return next;
        });
      }),
      eventsOn("pet:progress", (p: string) => {
        setMessages((prev) => [...prev, { role: "system", text: p }]);
      }),
      eventsOn("pet:tool", (name: string) => {
        setMessages((prev) => [...prev, { role: "system", text: `▸ ${name}` }]);
      }),
      eventsOn("pet:error", (msg: string) => {
        setMood("error");
        setMessages((prev) => [...prev, { role: "system", text: msg }]);
      }),
      eventsOn("pet:confirm", (p: ConfirmPayload) => {
        setConfirm(p);
        setExpanded(true);
      }),
      eventsOn("pet:choice", (p: any) => {
        setChoice(p);
        setExpanded(true);
      }),
      eventsOn("pet:done", () => {
        setBusy(false);
        assistantBuf.current = "";
        setConfirm(null);
      }),
      eventsOn("pet:ready", (st: any) => {
        if (st?.always_on_top != null) setAlwaysOnTop(!!st.always_on_top);
        void syncHits();
      }),
    ];
    return () => {
      offs.forEach((off) => off());
      stopVoiceRef.current?.();
    };
  }, [syncHits]);

  const openComposer = async () => {
    setExpanded(true);
    const a = await safeApp();
    await a?.ResizeWindow(380, 560);
    await syncHits();
  };

  const collapse = async () => {
    if (busy) return;
    stopVoiceRef.current?.();
    stopVoiceRef.current = null;
    setListening(false);
    setExpanded(false);
    const a = await safeApp();
    await a?.ResizeWindow(180, 200);
    await syncHits();
  };

  const send = async (text: string) => {
    const t = text.trim();
    if (!t || busy) return;
    const a = await safeApp();
    if (!a) {
      setMessages((prev) => [...prev, { role: "system", text: "bindings unavailable" }]);
      return;
    }
    setExpanded(true);
    setBusy(true);
    assistantBuf.current = "";
    setMessages((prev) => [...prev, { role: "user", text: t }]);
    setDraft("");
    try {
      await a.Send(t);
    } catch (e: any) {
      setBusy(false);
      setMood("error");
      setMessages((prev) => [...prev, { role: "system", text: String(e?.message || e) }]);
    }
  };

  const toggleVoice = () => {
    if (listening) {
      stopVoiceRef.current?.();
      stopVoiceRef.current = null;
      setListening(false);
      setMood("idle");
      setVoiceHint("");
      return;
    }
    if (busy) return;
    stopVoiceRef.current = startVoiceSession({
      onInterim: (t) => setDraft(t),
      onFinal: (t) => {
        stopVoiceRef.current = null;
        setListening(false);
        setVoiceHint("");
        setMood("idle");
        void send(t);
      },
      onStatus: (status, hint) => {
        if (hint) setVoiceHint(hint);
        if (status === "listening") {
          setListening(true);
          setMood("listening");
        } else if (status === "unsupported" || status === "error") {
          setListening(false);
          setMood(status === "error" ? "error" : "idle");
        } else if (status === "idle") {
          setListening(false);
          setMood((m) => (m === "listening" ? "idle" : m));
        }
      },
    });
  };

  return (
    <div className={`shell ${expanded ? "expanded" : "collapsed"}`} ref={shellRef}>
      <div className="pet-stage">
        <Cat
          mood={mood}
          onClick={() => {
            if (expanded) void collapse();
            else void openComposer();
          }}
        />
      </div>

      {expanded ? (
        <div className="panel" data-hit="panel">
          <div className="panel-bar">
            <span className="title">Cata</span>
            <label className="top-toggle">
              <input
                type="checkbox"
                checked={alwaysOnTop}
                onChange={async (e) => {
                  const on = e.target.checked;
                  setAlwaysOnTop(on);
                  const a = await safeApp();
                  await a?.SetAlwaysOnTop(on);
                }}
              />
              保持最前
            </label>
            <button type="button" className="ghost" onClick={() => void collapse()} disabled={busy}>
              收起
            </button>
          </div>

          <div className="messages">
            {messages.map((m, i) => (
              <div key={i} className={`msg ${m.role}`}>
                {m.text}
              </div>
            ))}
          </div>

          {confirm ? (
            <div className="overlay">
              <p>确认执行？</p>
              <code>{confirm.command_line}</code>
              <div className="row">
                <button
                  type="button"
                  onClick={async () => {
                    const a = await safeApp();
                    await a?.RespondExec(confirm.confirm_id, true);
                    setConfirm(null);
                  }}
                >
                  Run
                </button>
                <button
                  type="button"
                  onClick={async () => {
                    const a = await safeApp();
                    await a?.RespondExec(confirm.confirm_id, false);
                    setConfirm(null);
                  }}
                >
                  Cancel
                </button>
              </div>
            </div>
          ) : null}

          {choice ? (
            <div className="overlay">
              <p>{choice.prompt || "请选择"}</p>
              <div className="row wrap">
                {(choice.options || []).map((o: any) => (
                  <button
                    key={o.id}
                    type="button"
                    onClick={async () => {
                      const a = await safeApp();
                      await a?.RespondChoice(choice.id, [o.id]);
                      setChoice(null);
                    }}
                  >
                    {o.label || o.id}
                  </button>
                ))}
              </div>
            </div>
          ) : null}

          <div className="composer">
            <input
              value={draft}
              disabled={busy}
              placeholder="文字输入…"
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  void send(draft);
                }
              }}
            />
            <button
              type="button"
              className="mic"
              onClick={toggleVoice}
              title={voiceSupported ? "语音输入" : "当前环境不支持语音"}
              disabled={busy || !voiceSupported}
            >
              {listening ? "■" : "🎤"}
            </button>
            <button type="button" onClick={() => void send(draft)} disabled={busy || !draft.trim()}>
              发送
            </button>
          </div>
          {voiceHint ? <div className="hint">{voiceHint}</div> : null}
        </div>
      ) : null}
    </div>
  );
}
