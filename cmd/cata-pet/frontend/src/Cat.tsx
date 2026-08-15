import { useEffect, useRef } from "react";
import type { Mood } from "./wails";
import { safeApp } from "./wails";

type Props = {
  mood: Mood;
  onClick: () => void;
};

/**
 * Minimal chibi line cat — large head, tiny body, stroke only (matches brand sketch).
 */
export function Cat({ mood, onClick }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef({
    down: false,
    x: 0,
    y: 0,
    lastX: 0,
    lastY: 0,
    moved: false,
  });

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    let raf = 0;
    let t0 = performance.now();
    const tick = (now: number) => {
      const t = (now - t0) / 1000;
      let y = 0;
      let rot = 0;
      if (mood === "thinking" || mood === "listening") {
        y = Math.sin(t * 4) * 2.5;
        rot = Math.sin(t * 2.4) * 2;
      } else if (mood === "tool") {
        y = Math.sin(t * 7) * 1.5;
        rot = Math.sin(t * 9) * 4;
      } else if (mood === "idle") {
        y = Math.sin(t * 1.4) * 1.5;
      } else if (mood === "error") {
        rot = Math.sin(t * 18) * 3;
      }
      el.style.transform = `translate3d(0, ${y}px, 0) rotate(${rot}deg)`;
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [mood]);

  const eyesClosed = mood === "thinking" || mood === "listening";
  const eyesCross = mood === "error";
  const eyesWide = mood === "confirm";

  const stroke = 2.2;

  return (
    <div
      className="cat-hit"
      ref={wrapRef}
      data-hit="cat"
      onPointerDown={(e) => {
        (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
        dragRef.current = {
          down: true,
          x: e.screenX,
          y: e.screenY,
          lastX: e.screenX,
          lastY: e.screenY,
          moved: false,
        };
      }}
      onPointerMove={async (e) => {
        if (!dragRef.current.down) return;
        const dx = e.screenX - dragRef.current.x;
        const dy = e.screenY - dragRef.current.y;
        if (!dragRef.current.moved && Math.hypot(dx, dy) > 6) {
          dragRef.current.moved = true;
        }
        if (dragRef.current.moved) {
          const stepX = e.screenX - dragRef.current.lastX;
          const stepY = e.screenY - dragRef.current.lastY;
          dragRef.current.lastX = e.screenX;
          dragRef.current.lastY = e.screenY;
          const a = await safeApp();
          await a?.MoveWindowBy(stepX, stepY);
        }
      }}
      onPointerUp={() => {
        if (dragRef.current.down && !dragRef.current.moved) {
          onClick();
        }
        dragRef.current.down = false;
      }}
    >
      <svg
        className={`cat-svg mood-${mood}`}
        viewBox="0 0 100 120"
        width="120"
        height="144"
        aria-label="Cata"
        fill="none"
      >
        {/* head — wide, slightly squashed circle */}
        <ellipse
          cx="50"
          cy="42"
          rx="34"
          ry="30"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinecap="round"
        />

        {/* ears — small triangles, widely spaced */}
        <path
          d="M22 34 L26 12 L36 30"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
        <path
          d="M64 30 L74 12 L78 34"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {/* eyes */}
        {eyesCross ? (
          <>
            <path d="M38 44 L44 50 M44 44 L38 50" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
            <path d="M56 44 L62 50 M62 44 L56 50" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
          </>
        ) : eyesClosed ? (
          <>
            <path d="M36 46 Q41 43 46 46" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
            <path d="M54 46 Q59 43 64 46" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
          </>
        ) : (
          <>
            <circle cx="41" cy="46" r={eyesWide ? 2.8 : 2.2} fill="currentColor" stroke="none" />
            <circle cx="59" cy="46" r={eyesWide ? 2.8 : 2.2} fill="currentColor" stroke="none" />
          </>
        )}

        {/* mouth — tiny upward curve */}
        <path
          d="M47 54 Q50 56 53 54"
          stroke="currentColor"
          strokeWidth={1.6}
          strokeLinecap="round"
        />

        {/* body — small teardrop */}
        <path
          d="M50 72
             C42 72 36 82 38 94
             C40 102 46 106 50 108
             C54 106 60 102 62 94
             C64 82 58 72 50 72 Z"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinejoin="round"
        />

        {/* arms — thin curves outward */}
        <path
          d="M38 84 Q24 82 20 90"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinecap="round"
        />
        <path
          d="M62 84 Q76 82 80 90"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinecap="round"
        />

        {/* feet — small rounded bumps */}
        <path
          d="M42 106 Q44 110 46 106"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinecap="round"
        />
        <path
          d="M54 106 Q56 110 58 106"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinecap="round"
        />

        {/* tail — single curved line from lower right */}
        <path
          d="M62 98 Q78 92 82 76 Q84 66 76 58"
          stroke="currentColor"
          strokeWidth={stroke}
          strokeLinecap="round"
          className={mood === "tool" ? "tail-wag" : undefined}
        />
      </svg>
      {mood === "thinking" || mood === "listening" ? <div className="bubble-mini">…</div> : null}
      {mood === "confirm" ? <div className="bubble-mini">?</div> : null}
      {mood === "tool" ? <div className="bubble-mini">⚙</div> : null}
    </div>
  );
}
