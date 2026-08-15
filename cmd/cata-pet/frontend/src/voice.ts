/** Web Speech API helpers for pet voice input → same Send(text) path as typing. */

export type VoiceStatus = "idle" | "listening" | "unsupported" | "error";

type SpeechRec = {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  maxAlternatives: number;
  start(): void;
  stop(): void;
  abort(): void;
  onresult: ((ev: SpeechRecognitionEventLike) => void) | null;
  onerror: ((ev: { error?: string }) => void) | null;
  onend: (() => void) | null;
  onstart: (() => void) | null;
};

type SpeechRecognitionEventLike = {
  resultIndex: number;
  results: ArrayLike<{
    isFinal: boolean;
    0: { transcript: string };
  }>;
};

export function getSpeechRecognitionCtor(): (new () => SpeechRec) | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as {
    SpeechRecognition?: new () => SpeechRec;
    webkitSpeechRecognition?: new () => SpeechRec;
  };
  return w.SpeechRecognition || w.webkitSpeechRecognition || null;
}

export type VoiceSessionHandlers = {
  onInterim?: (text: string) => void;
  onFinal: (text: string) => void;
  onStatus: (status: VoiceStatus, hint?: string) => void;
};

/**
 * Start one-shot zh-CN recognition. Returns a stop() function.
 * Final transcript is passed to onFinal (caller should Send(text)).
 */
export function startVoiceSession(h: VoiceSessionHandlers): () => void {
  const Ctor = getSpeechRecognitionCtor();
  if (!Ctor) {
    h.onStatus("unsupported", "当前 WebView 不支持语音识别，请用文字（macOS 建议用 wails build 生成 .app 并授权麦克风）");
    return () => {};
  }

  const rec = new Ctor();
  rec.lang = "zh-CN";
  rec.continuous = false;
  rec.interimResults = true;
  rec.maxAlternatives = 1;

  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    try {
      rec.stop();
    } catch {
      try {
        rec.abort();
      } catch {
        /* ignore */
      }
    }
  };

  rec.onstart = () => h.onStatus("listening", "聆听中…再点麦克风结束");
  rec.onresult = (ev) => {
    let interim = "";
    let finalText = "";
    for (let i = ev.resultIndex; i < ev.results.length; i++) {
      const r = ev.results[i];
      const t = r[0]?.transcript || "";
      if (r.isFinal) finalText += t;
      else interim += t;
    }
    if (interim) h.onInterim?.(interim);
    if (finalText.trim()) {
      h.onFinal(finalText.trim());
      stop();
    }
  };
  rec.onerror = (ev) => {
    const err = ev.error || "error";
    const map: Record<string, string> = {
      "not-allowed": "麦克风/语音权限被拒绝，请在系统设置中允许",
      "service-not-allowed": "语音服务不可用",
      "no-speech": "没有听到声音，请重试",
      aborted: "",
      "network": "语音识别需要网络（部分引擎走云端）",
    };
    const hint = map[err] ?? `语音失败（${err}），请用文字`;
    if (err !== "aborted") h.onStatus("error", hint || undefined);
    else h.onStatus("idle");
  };
  rec.onend = () => {
    if (!stopped) h.onStatus("idle");
    stopped = true;
  };

  try {
    rec.start();
    h.onStatus("listening", "聆听中…");
  } catch {
    h.onStatus("error", "无法启动麦克风");
  }

  return stop;
}
